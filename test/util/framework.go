package util

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	kapiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

// WaitForServiceAccount waits until the named service account gets fully
// provisioned
func WaitForServiceAccount(c corev1client.ServiceAccountInterface, name string, checkSecret bool) error {
	countOutput := -1
	// add Logf for better debug, but it will possible generate many logs because of 100 millisecond
	// so, add countOutput so that it output log every 100 times (10s)
	waitFn := func(context.Context) (bool, error) {
		countOutput++
		sc, err := c.Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			// If we can't access the service accounts, let's wait till the controller
			// create it.
			if errors.IsNotFound(err) || errors.IsForbidden(err) {
				if countOutput%100 == 0 {
					e2e.Logf("Waiting for service account %q to be available: %v (will retry) ...", name, err)
				}
				return false, nil
			}
			return false, fmt.Errorf("failed to get service account %q: %v", name, err)
		}
		secretNames := []string{}
		var hasDockercfg bool
		for _, s := range sc.Secrets {
			if strings.Contains(s.Name, "dockercfg") {
				hasDockercfg = true
			}
			secretNames = append(secretNames, s.Name)
		}
		if hasDockercfg || !checkSecret {
			return true, nil
		}
		if countOutput%100 == 0 {
			e2e.Logf("Waiting for service account %q secrets (%s) to include dockercfg ...", name, strings.Join(secretNames, ","))
		}
		return false, nil
	}
	return wait.PollUntilContextTimeout(context.Background(), time.Duration(100*time.Millisecond), 3*time.Minute, true, waitFn)
}

// KubeConfigPath returns the value of KUBECONFIG environment variable
func KubeConfigPath() string {
	// can't use gomega in this method since it is used outside of It()
	return os.Getenv("KUBECONFIG")
}

// WaitForPods waits until given number of pods that match the label selector and
// satisfy the predicate are found
func WaitForPods(c corev1client.PodInterface, label labels.Selector, predicate func(kapiv1.Pod) bool, count int, timeout time.Duration) ([]string, error) {
	var podNames []string
	err := wait.PollUntilContextTimeout(context.Background(), 1*time.Second, timeout, true, func(context.Context) (bool, error) {

		p, e := GetPodNamesByFilter(c, label, predicate)
		if e != nil {
			return true, e
		}
		if len(p) != count {
			return false, nil
		}
		podNames = p
		return true, nil
	})
	return podNames, err
}

// GetPodNamesByFilter looks up pods that satisfy the predicate and returns their names.
func GetPodNamesByFilter(c corev1client.PodInterface, label labels.Selector, predicate func(kapiv1.Pod) bool) (podNames []string, err error) {
	podList, err := c.List(context.Background(), metav1.ListOptions{LabelSelector: label.String()})
	if err != nil {
		return nil, err
	}
	for _, pod := range podList.Items {
		if predicate(pod) {
			podNames = append(podNames, pod.Name)
		}
	}
	return podNames, nil
}

// CheckPodIsReady returns true if the pod's ready probe determined that the pod is ready.
func CheckPodIsReady(pod kapiv1.Pod) bool {
	if pod.Status.Phase != kapiv1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type != kapiv1.PodReady {
			continue
		}
		return cond.Status == kapiv1.ConditionTrue
	}
	return false
}

// ParseLabelsOrDie turns the given string into a label selector or
// panics; for tests or other cases where you know the string is valid.
// TODO: Move this to the upstream labels package.
func ParseLabelsOrDie(str string) labels.Selector {
	ret, err := labels.Parse(str)
	if err != nil {
		panic(fmt.Sprintf("cannot parse '%v': %v", str, err))
	}
	return ret
}
