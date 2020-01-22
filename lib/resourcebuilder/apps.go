package resourcebuilder

import (
	"context"
	"fmt"
	"strings"

	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/cluster-version-operator/pkg/manifest"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

type deploymentBuilder struct {
	client      *appsclientv1.AppsV1Client
	proxyGetter configv1.ProxiesGetter
	raw         []byte
	modifier    MetaV1ObjectModifierFunc
}

func newDeploymentBuilder(config *rest.Config, m manifest.Manifest) Interface {
	return &deploymentBuilder{
		client:      appsclientv1.NewForConfigOrDie(withProtobuf(config)),
		proxyGetter: configv1.NewForConfigOrDie(config),
		raw:         m.Raw,
	}
}

func (b *deploymentBuilder) WithMode(m Mode) Interface {
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
		proxyConfig, err := b.proxyGetter.Proxies().Get("cluster", metav1.GetOptions{})
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

	actual, updated, err := resourceapply.ApplyDeployment(b.client, deployment)
	if err != nil {
		return err
	}
	if updated && actual.Generation > 1 {
		return waitForDeploymentCompletion(ctx, b.client, deployment)
	}
	return nil
}
func waitForDeploymentCompletion(ctx context.Context, client appsclientv1.DeploymentsGetter, deployment *appsv1.Deployment) error {
	return wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		d, err := client.Deployments(deployment.Namespace).Get(deployment.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			// exit early to recreate the deployment.
			return false, err
		}
		if err != nil {
			// Do not return error here, as we could be updating the API Server itself, in which case we
			// want to continue waiting.
			klog.Errorf("error getting Deployment %s during rollout: %v", deployment.Name, err)
			return false, nil
		}

		if d.DeletionTimestamp != nil {
			return false, fmt.Errorf("Deployment %s is being deleted", deployment.Name)
		}

		if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedReplicas == d.Status.Replicas && d.Status.UnavailableReplicas == 0 {
			return true, nil
		}

		deploymentConditions := make([]string, 0, len(d.Status.Conditions))
		for _, dc := range d.Status.Conditions {
			switch dc.Type {
			case appsv1.DeploymentProgressing, appsv1.DeploymentAvailable:
				if dc.Status == "False" {
					deploymentConditions = append(deploymentConditions, fmt.Sprintf(", reason: %s, message: %s", dc.Reason, dc.Message))
				}
			case appsv1.DeploymentReplicaFailure:
				if dc.Status == "True" {
					deploymentConditions = append(deploymentConditions, fmt.Sprintf(", reason: %s, message: %s", dc.Reason, dc.Message))
				}
			}
		}

		klog.V(4).Infof("Deployment %s is not ready. status: (replicas: %d, updated: %d, ready: %d, unavailable: %d%s)",
			d.Name,
			d.Status.Replicas,
			d.Status.UpdatedReplicas,
			d.Status.ReadyReplicas,
			d.Status.UnavailableReplicas,
			strings.Join(deploymentConditions, ""))
		return false, nil
	}, ctx.Done())
}

type daemonsetBuilder struct {
	client      *appsclientv1.AppsV1Client
	proxyGetter configv1.ProxiesGetter
	raw         []byte
	modifier    MetaV1ObjectModifierFunc
}

func newDaemonsetBuilder(config *rest.Config, m manifest.Manifest) Interface {
	return &daemonsetBuilder{
		client:      appsclientv1.NewForConfigOrDie(withProtobuf(config)),
		proxyGetter: configv1.NewForConfigOrDie(config),
		raw:         m.Raw,
	}
}

func (b *daemonsetBuilder) WithMode(m Mode) Interface {
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
		proxyConfig, err := b.proxyGetter.Proxies().Get("cluster", metav1.GetOptions{})
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

	actual, updated, err := resourceapply.ApplyDaemonSet(b.client, daemonset)
	if err != nil {
		return err
	}
	if updated && actual.Generation > 1 {
		return waitForDaemonsetRollout(ctx, b.client, daemonset)
	}
	return nil
}

func waitForDaemonsetRollout(ctx context.Context, client appsclientv1.DaemonSetsGetter, daemonset *appsv1.DaemonSet) error {
	return wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		d, err := client.DaemonSets(daemonset.Namespace).Get(daemonset.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			// exit early to recreate the daemonset.
			return false, err
		}
		if err != nil {
			// Do not return error here, as we could be updating the API Server itself, in which case we
			// want to continue waiting.
			klog.Errorf("error getting Daemonset %s during rollout: %v", daemonset.Name, err)
			return false, nil
		}

		if d.DeletionTimestamp != nil {
			return false, fmt.Errorf("Daemonset %s is being deleted", daemonset.Name)
		}

		if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedNumberScheduled == d.Status.DesiredNumberScheduled && d.Status.NumberUnavailable == 0 {
			return true, nil
		}
		klog.V(4).Infof("Daemonset %s is not ready. status: (desired: %d, updated: %d, ready: %d, unavailable: %d)", d.Name, d.Status.DesiredNumberScheduled, d.Status.UpdatedNumberScheduled, d.Status.NumberReady, d.Status.NumberAvailable)
		return false, nil
	}, ctx.Done())
}
