package internal

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

var (
	osScheme = runtime.NewScheme()
	osCodecs = serializer.NewCodecFactory(osScheme)

	osMapper = resourcebuilder.NewResourceMapper()
)

func init() {
	if err := configv1.AddToScheme(osScheme); err != nil {
		panic(err)
	}

	osMapper.RegisterGVK(configv1.SchemeGroupVersion.WithKind("ClusterOperator"), newClusterOperatorBuilder)
	osMapper.AddToMap(resourcebuilder.Mapper)
}

// readClusterOperatorV1OrDie reads clusteroperator object from bytes. Panics on error.
func readClusterOperatorV1OrDie(objBytes []byte) *configv1.ClusterOperator {
	requiredObj, err := runtime.Decode(osCodecs.UniversalDecoder(configv1.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*configv1.ClusterOperator)
}

type clusterOperatorBuilder struct {
	client   configclientv1.ConfigV1Interface
	raw      []byte
	modifier resourcebuilder.MetaV1ObjectModifierFunc
}

func newClusterOperatorBuilder(config *rest.Config, m lib.Manifest) resourcebuilder.Interface {
	return &clusterOperatorBuilder{
		client: configclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *clusterOperatorBuilder) WithModifier(f resourcebuilder.MetaV1ObjectModifierFunc) resourcebuilder.Interface {
	b.modifier = f
	return b
}

func (b *clusterOperatorBuilder) Do() error {
	os := readClusterOperatorV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(os)
	}

	return waitForOperatorStatusToBeDone(b.client, os)
}

const (
	osPollInternal = 1 * time.Second
	osPollTimeout  = 1 * time.Minute
)

func waitForOperatorStatusToBeDone(client configclientv1.ClusterOperatorsGetter, co *configv1.ClusterOperator) error {
	var lastCO *configv1.ClusterOperator
	err := wait.Poll(osPollInternal, osPollTimeout, func() (bool, error) {
		eos, err := client.ClusterOperators().Get(co.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			if isRetriableError(err) {
				return false, nil
			}
			return false, err
		}
		lastCO = eos

		// TODO: temporarily disabled while the design for version tracking is finalized
		// if eos.Status.Version != os.Status.Version {
		// 	return false, nil
		// }

		available := false
		progressing := true
		failing := true
		for _, condition := range eos.Status.Conditions {
			switch {
			case condition.Type == configv1.OperatorAvailable && condition.Status == configv1.ConditionTrue:
				available = true
			case condition.Type == configv1.OperatorProgressing && condition.Status == configv1.ConditionFalse:
				progressing = false
			case condition.Type == configv1.OperatorFailing && condition.Status == configv1.ConditionFalse:
				failing = false
			}
		}
		// if we're at the correct version, and available, not progressing, and not failing, we are done
		if available && !progressing && !failing {
			return true, nil
		}
		return false, nil
	})

	if err != wait.ErrWaitTimeout {
		return err
	}

	if lastCO == nil {
		return &payload.UpdateError{
			Reason:  "ClusterOperatorNotAvailable",
			Message: fmt.Sprintf("Cluster operator %s has not yet reported success", co.Name),
			Name:    co.Name,
		}
	}
	co = lastCO

	glog.V(3).Infof("ClusterOperator %s is not done; it is available=%v, progressing=%v, failing=%v",
		co.Name,
		resourcemerge.IsOperatorStatusConditionTrue(co.Status.Conditions, configv1.OperatorAvailable),
		resourcemerge.IsOperatorStatusConditionTrue(co.Status.Conditions, configv1.OperatorProgressing),
		resourcemerge.IsOperatorStatusConditionTrue(co.Status.Conditions, configv1.OperatorFailing),
	)

	if c := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, configv1.OperatorFailing); c != nil && c.Status == configv1.ConditionTrue {
		if len(c.Message) > 0 {
			return &payload.UpdateError{
				Reason:  "ClusterOperatorFailing",
				Message: fmt.Sprintf("Cluster operator %s is reporting a failure: %s", co.Name, c.Message),
				Name:    co.Name,
			}
		}
		return &payload.UpdateError{
			Reason:  "ClusterOperatorFailing",
			Message: fmt.Sprintf("Cluster operator %s is reporting a failure", co.Name),
			Name:    co.Name,
		}
	}
	return &payload.UpdateError{
		Reason:  "ClusterOperatorNotAvailable",
		Message: fmt.Sprintf("Cluster operator %s has not yet reported success", co.Name),
		Name:    co.Name,
	}
}

// isRetriableError returns true if the error we encounter can be retried safely.
func isRetriableError(err error) bool {
	// the CVO typically talks to the apiserver via localhost, which means a restart can
	// result in temporary connection refused errors
	return isConnectionRefused(err)
}

func isConnectionRefused(err error) bool {
	if urlErr, ok := err.(*url.Error); ok {
		err = urlErr.Err
	}
	if opErr, ok := err.(*net.OpError); ok {
		err = opErr.Err
	}
	if osErr, ok := err.(*os.SyscallError); ok {
		err = osErr.Err
	}
	if errno, ok := err.(syscall.Errno); ok && errno == syscall.ECONNREFUSED {
		return true
	}
	return false
}
