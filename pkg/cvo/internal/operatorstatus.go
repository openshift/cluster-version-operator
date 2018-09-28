package internal

import (
	"time"

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

	osMapper.RegisterGVK(osv1.SchemeGroupVersion.WithKind("OperatorStatus"), newOperatorStatusBuilder)
	osMapper.AddToMap(resourcebuilder.Mapper)
}

// readOperatorStatusV1OrDie reads operatorstatus object from bytes. Panics on error.
func readOperatorStatusV1OrDie(objBytes []byte) *osv1.OperatorStatus {
	requiredObj, err := runtime.Decode(osCodecs.UniversalDecoder(osv1.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*osv1.OperatorStatus)
}

type operatorStatusBuilder struct {
	client   *osclientv1.OperatorstatusV1Client
	raw      []byte
	modifier resourcebuilder.MetaV1ObjectModifierFunc
}

func newOperatorStatusBuilder(config *rest.Config, m lib.Manifest) resourcebuilder.Interface {
	return &operatorStatusBuilder{
		client: osclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *operatorStatusBuilder) WithModifier(f resourcebuilder.MetaV1ObjectModifierFunc) resourcebuilder.Interface {
	b.modifier = f
	return b
}

func (b *operatorStatusBuilder) Do() error {
	os := readOperatorStatusV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(os)
	}

	return waitForOperatorStatusToBeDone(b.client, os)
}

const (
	osPollInternal = 1 * time.Second
	osPollTimeout  = 1 * time.Minute
)

func waitForOperatorStatusToBeDone(client osclientv1.OperatorStatusesGetter, os *osv1.OperatorStatus) error {
	return wait.Poll(osPollInternal, osPollTimeout, func() (bool, error) {
		eos, err := client.OperatorStatuses(os.Namespace).Get(os.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if eos.Version == os.Version && eos.Condition.Type == osv1.OperatorStatusConditionTypeDone {
			return true, nil
		}
		glog.V(4).Infof("OperatorStatus %s/%s is not reporting %s for version %s; it is reporting %s for version %s",
			eos.Namespace, eos.Name,
			osv1.OperatorStatusConditionTypeDone, os.Version,
			eos.Condition.Type, eos.Version,
		)
		return false, nil
	})
}
