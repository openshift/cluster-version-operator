package resourcebuilder

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

type deploymentBuilder struct {
	client               *appsclientv1.AppsV1Client
	proxyGetter          configv1.ProxiesGetter
	infrastructureGetter configv1.InfrastructuresGetter
	raw                  []byte
	modifier             MetaV1ObjectModifierFunc
	mode                 Mode
}

func newDeploymentBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &deploymentBuilder{
		client:               appsclientv1.NewForConfigOrDie(withProtobuf(config)),
		proxyGetter:          configv1.NewForConfigOrDie(config),
		infrastructureGetter: configv1.NewForConfigOrDie(config),
		raw:                  m.Raw,
	}
}

func (b *deploymentBuilder) WithMode(m Mode) Interface {
	b.mode = m
	return b
}

func (b *deploymentBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *deploymentBuilder) Do(ctx context.Context) error {
	deployment := resourceread.ReadDeploymentV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(deployment)
	}

	// if proxy injection is requested, get the proxy values and use them
	if containerNamesString := deployment.Annotations["config.openshift.io/inject-proxy"]; len(containerNamesString) > 0 {
		proxyConfig, err := b.proxyGetter.Proxies().Get(ctx, "cluster", metav1.GetOptions{})
		// not found just means that we don't have proxy configuration, so we should tolerate and fill in empty
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		err = updatePodSpecWithProxy(
			&deployment.Spec.Template.Spec,
			strings.Split(containerNamesString, ","),
			proxyConfig.Status.HTTPProxy,
			proxyConfig.Status.HTTPSProxy,
			proxyConfig.Status.NoProxy,
		)
		if err != nil {
			return err
		}
	}

	// if we detect the CVO deployment we need to replace the KUBERNETES_SERVICE_HOST env var with the internal load
	// balancer to be resilient to kube-apiserver rollouts that cause the localhost server to become non-responsive for
	// multiple minutes.
	if deployment.Namespace == "openshift-cluster-version" && deployment.Name == "cluster-version-operator" {
		infrastructureConfig, err := b.infrastructureGetter.Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
		// not found just means that we don't have infrastructure configuration yet, so we should tolerate not found and avoid substitution
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		if !errors.IsNotFound(err) {
			lbURL, err := url.Parse(infrastructureConfig.Status.APIServerInternalURL)
			if err != nil {
				return err
			}
			// if we have any error and have empty strings, substitution below will do nothing and leave the manifest specified value
			// errors can happen when the port is not specified, in which case we have a host and we write that into the env vars
			lbHost, lbPort, err := net.SplitHostPort(lbURL.Host)
			if err != nil {
				if strings.Contains(err.Error(), "missing port in address") {
					lbHost = lbURL.Host
					lbPort = ""
				} else {
					return err
				}
			}
			err = updatePodSpecWithInternalLoadBalancerKubeService(
				&deployment.Spec.Template.Spec,
				[]string{"cluster-version-operator"},
				lbHost,
				lbPort,
			)
			if err != nil {
				return err
			}
		}
	}

	if _, _, err := resourceapply.ApplyDeployment(ctx, b.client, deployment); err != nil {
		return err
	}

	if b.mode != InitializingMode {
		return checkDeploymentHealth(ctx, b.client, deployment)
	}
	return nil
}

func checkDeploymentHealth(ctx context.Context, client appsclientv1.DeploymentsGetter, deployment *appsv1.Deployment) error {
	iden := fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)
	d, err := client.Deployments(deployment.Namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if d.DeletionTimestamp != nil {
		return fmt.Errorf("deployment %s is being deleted", iden)
	}

	var availableCondition *appsv1.DeploymentCondition
	var progressingCondition *appsv1.DeploymentCondition
	var replicaFailureCondition *appsv1.DeploymentCondition
	for idx, dc := range d.Status.Conditions {
		switch dc.Type {
		case appsv1.DeploymentProgressing:
			progressingCondition = &d.Status.Conditions[idx]
		case appsv1.DeploymentAvailable:
			availableCondition = &d.Status.Conditions[idx]
		case appsv1.DeploymentReplicaFailure:
			replicaFailureCondition = &d.Status.Conditions[idx]
		}
	}

	if replicaFailureCondition != nil && replicaFailureCondition.Status == corev1.ConditionTrue {
		return &payload.UpdateError{
			Nested:  fmt.Errorf("deployment %s has some pods failing; unavailable replicas=%d", iden, d.Status.UnavailableReplicas),
			Reason:  "WorkloadNotProgressing",
			Message: fmt.Sprintf("deployment %s has a replica failure %s: %s", iden, replicaFailureCondition.Reason, replicaFailureCondition.Message),
			Name:    iden,
		}
	}

	if availableCondition != nil && availableCondition.Status == corev1.ConditionFalse {
		return &payload.UpdateError{
			Nested:  fmt.Errorf("deployment %s is not available; updated replicas=%d of %d, available replicas=%d of %d", iden, d.Status.UpdatedReplicas, d.Status.Replicas, d.Status.AvailableReplicas, d.Status.Replicas),
			Reason:  "WorkloadNotAvailable",
			Message: fmt.Sprintf("deployment %s is not available %s: %s", iden, availableCondition.Reason, availableCondition.Message),
			Name:    iden,
		}
	}

	if progressingCondition != nil && progressingCondition.Status == corev1.ConditionFalse {
		return &payload.UpdateError{
			Nested:  fmt.Errorf("deployment %s is not progressing; updated replicas=%d of %d, available replicas=%d of %d", iden, d.Status.UpdatedReplicas, d.Status.Replicas, d.Status.AvailableReplicas, d.Status.Replicas),
			Reason:  "WorkloadNotAvailable",
			Message: fmt.Sprintf("deployment %s is not progressing %s: %s", iden, progressingCondition.Reason, progressingCondition.Message),
			Name:    iden,
		}
	}

	if availableCondition == nil && progressingCondition == nil && replicaFailureCondition == nil {
		klog.Warningf("deployment %s is not setting any expected conditions, and is therefore in an unknown state", iden)
	}

	return nil
}

type daemonsetBuilder struct {
	client      *appsclientv1.AppsV1Client
	proxyGetter configv1.ProxiesGetter
	raw         []byte
	modifier    MetaV1ObjectModifierFunc
	mode        Mode
}

func newDaemonsetBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &daemonsetBuilder{
		client:      appsclientv1.NewForConfigOrDie(withProtobuf(config)),
		proxyGetter: configv1.NewForConfigOrDie(config),
		raw:         m.Raw,
	}
}

func (b *daemonsetBuilder) WithMode(m Mode) Interface {
	b.mode = m
	return b
}

func (b *daemonsetBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *daemonsetBuilder) Do(ctx context.Context) error {
	daemonset := resourceread.ReadDaemonSetV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(daemonset)
	}

	// if proxy injection is requested, get the proxy values and use them
	if containerNamesString := daemonset.Annotations["config.openshift.io/inject-proxy"]; len(containerNamesString) > 0 {
		proxyConfig, err := b.proxyGetter.Proxies().Get(ctx, "cluster", metav1.GetOptions{})
		// not found just means that we don't have proxy configuration, so we should tolerate and fill in empty
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		err = updatePodSpecWithProxy(
			&daemonset.Spec.Template.Spec,
			strings.Split(containerNamesString, ","),
			proxyConfig.Status.HTTPProxy,
			proxyConfig.Status.HTTPSProxy,
			proxyConfig.Status.NoProxy,
		)
		if err != nil {
			return err
		}
	}

	if _, _, err := resourceapply.ApplyDaemonSet(ctx, b.client, daemonset); err != nil {
		return err
	}

	if b.mode != InitializingMode {
		return checkDaemonSetHealth(ctx, b.client, daemonset)
	}

	return nil
}

func checkDaemonSetHealth(ctx context.Context, client appsclientv1.DaemonSetsGetter, daemonset *appsv1.DaemonSet) error {
	iden := fmt.Sprintf("%s/%s", daemonset.Namespace, daemonset.Name)
	d, err := client.DaemonSets(daemonset.Namespace).Get(ctx, daemonset.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if d.DeletionTimestamp != nil {
		return fmt.Errorf("daemonset %s is being deleted", iden)
	}

	// Kubernetes DaemonSet controller doesn't set status conditions yet (v1.18.0), so nothing more to check.
	return nil
}
