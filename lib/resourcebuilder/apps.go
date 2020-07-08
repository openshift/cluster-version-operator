package resourcebuilder

import (
	"context"
	"fmt"
	"strings"

	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

type deploymentBuilder struct {
	client      *appsclientv1.AppsV1Client
	proxyGetter configv1.ProxiesGetter
	raw         []byte
	modifier    MetaV1ObjectModifierFunc
	mode        Mode
}

func newDeploymentBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &deploymentBuilder{
		client:      appsclientv1.NewForConfigOrDie(withProtobuf(config)),
		proxyGetter: configv1.NewForConfigOrDie(config),
		raw:         m.Raw,
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

	_, updated, err := resourceapply.ApplyDeployment(b.client, deployment)
	if err != nil {
		return err
	}
	if updated && b.mode != InitializingMode {
		return waitForDeploymentCompletion(ctx, b.client, deployment)
	}
	return nil
}
func waitForDeploymentCompletion(ctx context.Context, client appsclientv1.DeploymentsGetter, deployment *appsv1.Deployment) error {
	iden := fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)
	var lastErr error
	err := wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		d, err := client.Deployments(deployment.Namespace).Get(deployment.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			// exit early to recreate the deployment.
			return false, err
		}
		if err != nil {
			// Do not return error here, as we could be updating the API Server itself, in which case we
			// want to continue waiting.
			lastErr = &payload.UpdateError{
				Nested:  err,
				Reason:  "WorkloadNotAvailable",
				Message: fmt.Sprintf("could not find the deployment %s during rollout", iden),
				Name:    iden,
			}
			return false, nil
		}

		if d.DeletionTimestamp != nil {
			return false, fmt.Errorf("Deployment %s is being deleted", iden)
		}

		if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedReplicas == d.Status.Replicas && d.Status.UnavailableReplicas == 0 {
			return true, nil
		}

		var availableCondition *appsv1.DeploymentCondition
		var progressingCondition *appsv1.DeploymentCondition
		var replicafailureCondition *appsv1.DeploymentCondition
		for idx, dc := range d.Status.Conditions {
			switch dc.Type {
			case appsv1.DeploymentProgressing:
				progressingCondition = &d.Status.Conditions[idx]
			case appsv1.DeploymentAvailable:
				availableCondition = &d.Status.Conditions[idx]
			case appsv1.DeploymentReplicaFailure:
				replicafailureCondition = &d.Status.Conditions[idx]
			}
		}

		if replicafailureCondition != nil && replicafailureCondition.Status == corev1.ConditionTrue {
			lastErr = &payload.UpdateError{
				Nested:  fmt.Errorf("deployment %s has some pods failing; unavailable replicas=%d", iden, d.Status.UnavailableReplicas),
				Reason:  "WorkloadNotProgressing",
				Message: fmt.Sprintf("deployment %s has a replica failure %s: %s", iden, replicafailureCondition.Reason, replicafailureCondition.Message),
				Name:    iden,
			}
			return false, nil
		}

		if availableCondition != nil && availableCondition.Status == corev1.ConditionFalse {
			lastErr = &payload.UpdateError{
				Nested:  fmt.Errorf("deployment %s is not available; updated replicas=%d of %d, available replicas=%d of %d", iden, d.Status.UpdatedReplicas, d.Status.Replicas, d.Status.AvailableReplicas, d.Status.Replicas),
				Reason:  "WorkloadNotAvailable",
				Message: fmt.Sprintf("deployment %s is not available %s: %s", iden, availableCondition.Reason, availableCondition.Message),
				Name:    iden,
			}
			return false, nil
		}

		if progressingCondition != nil && progressingCondition.Status == corev1.ConditionFalse && progressingCondition.Reason == "ProgressDeadlineExceeded" {
			lastErr = &payload.UpdateError{
				Nested:  fmt.Errorf("deployment %s is not progressing; updated replicas=%d of %d, available replicas=%d of %d", iden, d.Status.UpdatedReplicas, d.Status.Replicas, d.Status.AvailableReplicas, d.Status.Replicas),
				Reason:  "WorkloadNotAvailable",
				Message: fmt.Sprintf("deployment %s is not progressing %s: %s", iden, progressingCondition.Reason, progressingCondition.Message),
				Name:    iden,
			}
			return false, nil
		}

		if progressingCondition != nil && progressingCondition.Status == corev1.ConditionTrue {
			klog.V(4).Infof("deployment %s is progressing", iden)
			return false, nil
		}

		klog.Errorf("deployment %s is in unknown state", iden)
		return false, nil
	}, ctx.Done())
	if err != nil {
		if err == wait.ErrWaitTimeout && lastErr != nil {
			return lastErr
		}
		return err
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

	_, updated, err := resourceapply.ApplyDaemonSet(b.client, daemonset)
	if err != nil {
		return err
	}
	if updated && b.mode != InitializingMode {
		return waitForDaemonsetRollout(ctx, b.client, daemonset)
	}
	return nil
}

func waitForDaemonsetRollout(ctx context.Context, client appsclientv1.DaemonSetsGetter, daemonset *appsv1.DaemonSet) error {
	iden := fmt.Sprintf("%s/%s", daemonset.Namespace, daemonset.Name)
	var lastErr error
	err := wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		d, err := client.DaemonSets(daemonset.Namespace).Get(daemonset.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			// exit early to recreate the daemonset.
			return false, err
		}
		if err != nil {
			// Do not return error here, as we could be updating the API Server itself, in which case we
			// want to continue waiting.
			lastErr = &payload.UpdateError{
				Nested:  err,
				Reason:  "WorkloadNotAvailable",
				Message: fmt.Sprintf("could not find the daemonset %s during rollout", iden),
				Name:    iden,
			}
			return false, nil
		}

		if d.DeletionTimestamp != nil {
			return false, fmt.Errorf("Daemonset %s is being deleted", daemonset.Name)
		}

		if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedNumberScheduled == d.Status.DesiredNumberScheduled && d.Status.NumberUnavailable == 0 {
			return true, nil
		}
		klog.V(4).Infof("daemonset %s is progressing", iden)
		return false, nil
	}, ctx.Done())
	if err != nil {
		if err == wait.ErrWaitTimeout && lastErr != nil {
			return lastErr
		}
		return err
	}
	return nil
}
