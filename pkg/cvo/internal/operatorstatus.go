package internal

import (
	"time"

	"github.com/davecgh/go-spew/spew"

	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	osclientv1 "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned/typed/operatorstatus.openshift.io/v1"
)

var (
	osScheme = runtime.NewScheme()
	osCodecs = serializer.NewCodecFactory(osScheme)

	osMapper = resourcebuilder.NewResourceMapper()
)

func init() {
	if err := osv1.AddToScheme(osScheme); err != nil {
		panic(err)
	}

	osMapper.RegisterGVK(osv1.SchemeGroupVersion.WithKind("ClusterOperator"), newClusterOperatorBuilder)
	osMapper.AddToMap(resourcebuilder.Mapper)
}

// readClusterOperatorV1OrDie reads clusteroperator object from bytes. Panics on error.
func readClusterOperatorV1OrDie(objBytes []byte) *osv1.ClusterOperator {
	requiredObj, err := runtime.Decode(osCodecs.UniversalDecoder(osv1.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*osv1.ClusterOperator)
}

type clusterOperatorBuilder struct {
	client   *osclientv1.OperatorstatusV1Client
	raw      []byte
	modifier resourcebuilder.MetaV1ObjectModifierFunc
}

func newClusterOperatorBuilder(config *rest.Config, m lib.Manifest) resourcebuilder.Interface {
	return &clusterOperatorBuilder{
		client: osclientv1.NewForConfigOrDie(config),
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

func waitForOperatorStatusToBeDone(client osclientv1.ClusterOperatorsGetter, os *osv1.ClusterOperator) error {
	return wait.Poll(osPollInternal, osPollTimeout, func() (bool, error) {
		eos, err := client.ClusterOperators(os.Namespace).Get(os.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		glog.V(4).Infof("ClusterOperator %s/%s is reporting %v",
			eos.Namespace, eos.Name, spew.Sdump(eos.Status))

		if eos.Status.Version != os.Status.Version {
			return false, nil
		}

		available := false
		progressing := true
		failing := true
		for _, condition := range eos.Status.Conditions {
			switch {
			case condition.Type == osv1.OperatorAvailable && condition.Status == osv1.ConditionTrue:
				available = true
			case condition.Type == osv1.OperatorProgressing && condition.Status == osv1.ConditionFalse:
				progressing = false
			case condition.Type == osv1.OperatorFailing && condition.Status == osv1.ConditionFalse:
				failing = false
			}
		}

		// if we're at the correct version, and available, not progressing, and not failing, we are done
		if available && !progressing && !failing {
			return true, nil
		}
		glog.V(3).Infof("ClusterOperator %s/%s is not done for version %s; it is version=%v, available=%v, progressing=%v, failing=%v",
			eos.Namespace, eos.Name, os.Status.Version,
			eos.Status.Version, available, progressing, failing)

		return false, nil
	})
}
