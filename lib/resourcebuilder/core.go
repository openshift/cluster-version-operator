package resourcebuilder

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/golang/glog"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
)

type serviceAccountBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newServiceAccountBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &serviceAccountBuilder{
		client: coreclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *serviceAccountBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *serviceAccountBuilder) Do() error {
	serviceAccount := resourceread.ReadServiceAccountV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(serviceAccount)
	}
	_, _, err := resourceapply.ApplyServiceAccount(b.client, serviceAccount)
	return err
}

type configMapBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newConfigMapBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &configMapBuilder{
		client: coreclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *configMapBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *configMapBuilder) Do() error {
	configMap := resourceread.ReadConfigMapV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(configMap)
	}
	_, _, err := resourceapply.ApplyConfigMap(b.client, configMap)
	return err
}

type namespaceBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newNamespaceBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &namespaceBuilder{
		client: coreclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *namespaceBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *namespaceBuilder) Do() error {
	namespace := resourceread.ReadNamespaceV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(namespace)
	}
	_, _, err := resourceapply.ApplyNamespace(b.client, namespace)
	return err
}

type serviceBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newServiceBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &serviceBuilder{
		client: coreclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *serviceBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *serviceBuilder) Do() error {
	service := resourceread.ReadServiceV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(service)
	}
	_, _, err := resourceapply.ApplyService(b.client, service)
	return err
}

// WaitForPodSuccess waits for pod to complete successfully and returns an error if it doesn't.
// It will always return the last valid pod it had for the caller to check additional information.
func WaitForPodSuccess(client coreclientv1.PodsGetter, pod *corev1.Pod) (*corev1.Pod, error) {
	var lastPod *corev1.Pod
	err := wait.Poll(jobPollInterval, jobPollTimeout, func() (bool, error) {
		p, err := client.Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
		if err != nil {
			glog.Errorf("error getting Pod %s: %v", pod.Name, err)
			return false, nil
		}

		lastPod = p

		switch p.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodPending, corev1.PodRunning, corev1.PodUnknown:
			return false, nil
		case corev1.PodFailed:
			msg := "unknown error"
			if len(p.Status.Message) > 0 {
				msg = p.Status.Message
			}
			return false, fmt.Errorf("pod failed: %v", msg)
		}
		return false, nil
	})
	if err != nil {
		return lastPod, err
	}
	return lastPod, nil
}
