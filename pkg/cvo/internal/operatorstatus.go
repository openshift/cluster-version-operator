package internal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/library-go/pkg/manifest"
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
	client       ClusterOperatorsGetter
	createClient configclientv1.ClusterOperatorInterface
	raw          []byte
	modifier     resourcebuilder.MetaV1ObjectModifierFunc
	mode         resourcebuilder.Mode
}

func newClusterOperatorBuilder(config *rest.Config, m manifest.Manifest) resourcebuilder.Interface {
	client := configclientv1.NewForConfigOrDie(config).ClusterOperators()
	return NewClusterOperatorBuilder(clientClusterOperatorsGetter{getter: client}, client, m)
}

// ClusterOperatorsGetter abstracts object access with a client or a cache lister.
type ClusterOperatorsGetter interface {
	Get(ctx context.Context, name string) (*configv1.ClusterOperator, error)
}

type clientClusterOperatorsGetter struct {
	getter configclientv1.ClusterOperatorInterface
}

func (g clientClusterOperatorsGetter) Get(ctx context.Context, name string) (*configv1.ClusterOperator, error) {
	return g.getter.Get(ctx, name, metav1.GetOptions{})
}

// NewClusterOperatorBuilder accepts the ClusterOperatorsGetter interface which may be implemented by a
// client or a lister cache.
func NewClusterOperatorBuilder(client ClusterOperatorsGetter, createClient configclientv1.ClusterOperatorInterface, m manifest.Manifest) resourcebuilder.Interface {
	return &clusterOperatorBuilder{
		client:       client,
		createClient: createClient,
		raw:          m.Raw,
	}
}

func (b *clusterOperatorBuilder) WithMode(m resourcebuilder.Mode) resourcebuilder.Interface {
	b.mode = m
	return b
}

func (b *clusterOperatorBuilder) WithModifier(f resourcebuilder.MetaV1ObjectModifierFunc) resourcebuilder.Interface {
	b.modifier = f
	return b
}

func (b *clusterOperatorBuilder) Do(ctx context.Context) error {
	os := readClusterOperatorV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(os)
	}

	// create the object, and if we successfully created, update the status
	if b.mode == resourcebuilder.PrecreatingMode {
		clusterOperator, err := b.createClient.Create(ctx, os, metav1.CreateOptions{})
		if err != nil {
			if kerrors.IsAlreadyExists(err) {
				return nil
			}
			return err
		}
		clusterOperator.Status.RelatedObjects = os.Status.DeepCopy().RelatedObjects
		if _, err := b.createClient.UpdateStatus(ctx, clusterOperator, metav1.UpdateOptions{}); err != nil {
			if kerrors.IsConflict(err) {
				return nil
			}
			return err
		}
		return nil
	}

	return waitForOperatorStatusToBeDone(ctx, 1*time.Second, b.client, os, b.mode)
}

func waitForOperatorStatusToBeDone(ctx context.Context, interval time.Duration, client ClusterOperatorsGetter, expected *configv1.ClusterOperator, mode resourcebuilder.Mode) error {
	var lastErr error
	err := wait.PollImmediateUntil(interval, func() (bool, error) {
		actual, err := client.Get(ctx, expected.Name)
		if err != nil {
			lastErr = &payload.UpdateError{
				Nested:  err,
				Reason:  "ClusterOperatorNotAvailable",
				Message: fmt.Sprintf("Cluster operator %s has not yet reported success", expected.Name),
				Name:    expected.Name,
			}
			return false, nil
		}

		// undone is map of operand to tuple of (expected version, actual version)
		// for incomplete operands.
		undone := map[string][]string{}
		for _, expOp := range expected.Status.Versions {
			undone[expOp.Name] = []string{expOp.Version}
			for _, actOp := range actual.Status.Versions {
				if actOp.Name == expOp.Name {
					undone[expOp.Name] = append(undone[expOp.Name], actOp.Version)
					if actOp.Version == expOp.Version {
						delete(undone, expOp.Name)
					}
					break
				}
			}
		}
		if len(undone) > 0 {
			var keys []string
			for k := range undone {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			message := fmt.Sprintf("Cluster operator %s is still updating", actual.Name)
			lastErr = &payload.UpdateError{
				Nested:  errors.New(lowerFirst(message)),
				Reason:  "ClusterOperatorNotAvailable",
				Message: message,
				Name:    actual.Name,
			}
			return false, nil
		}

		available := false
		progressing := true
		failing := true
		var failingCondition *configv1.ClusterOperatorStatusCondition
		degradedValue := true
		var degradedCondition *configv1.ClusterOperatorStatusCondition
		for i := range actual.Status.Conditions {
			condition := &actual.Status.Conditions[i]
			switch {
			case condition.Type == configv1.OperatorAvailable && condition.Status == configv1.ConditionTrue:
				available = true
			case condition.Type == configv1.OperatorProgressing && condition.Status == configv1.ConditionFalse:
				progressing = false
			case condition.Type == configv1.OperatorDegraded:
				if condition.Status == configv1.ConditionFalse {
					degradedValue = false
				}
				degradedCondition = condition
			}
		}

		// If degraded was an explicitly set condition, use that. If not, use the deprecated failing.
		degraded := failing
		if degradedCondition != nil {
			degraded = degradedValue
		}

		switch mode {
		case resourcebuilder.InitializingMode:
			// during initialization we allow degraded as long as the component goes available
			if available && (!progressing || len(expected.Status.Versions) > 0) {
				return true, nil
			}
		default:
			// if we're at the correct version, and available, and not degraded, we are done
			// if we're available, not degraded, and not progressing, we're also done
			// TODO: remove progressing once all cluster operators report expected versions
			if available && (!progressing || len(expected.Status.Versions) > 0) && !degraded {
				return true, nil
			}
		}

		condition := failingCondition
		if degradedCondition != nil {
			condition = degradedCondition
		}
		if condition != nil && condition.Status == configv1.ConditionTrue {
			message := fmt.Sprintf("Cluster operator %s is reporting a failure", actual.Name)
			if len(condition.Message) > 0 {
				message = fmt.Sprintf("Cluster operator %s is reporting a failure: %s", actual.Name, condition.Message)
			}
			lastErr = &payload.UpdateError{
				Nested:  errors.New(lowerFirst(message)),
				Reason:  "ClusterOperatorDegraded",
				Message: message,
				Name:    actual.Name,
			}
			return false, nil
		}

		lastErr = &payload.UpdateError{
			Nested: fmt.Errorf("cluster operator %s is not done; it is available=%v, progressing=%v, degraded=%v",
				actual.Name, available, progressing, degraded,
			),
			Reason:  "ClusterOperatorNotAvailable",
			Message: fmt.Sprintf("Cluster operator %s has not yet reported success", actual.Name),
			Name:    actual.Name,
		}
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

func lowerFirst(str string) string {
	for i, v := range str {
		return string(unicode.ToLower(v)) + str[i+1:]
	}
	return ""
}
