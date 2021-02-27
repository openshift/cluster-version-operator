package internal

import (
	"context"
	"fmt"
	"sort"
	"strings"
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

	// add cluster operator's start time if not already there
	payload.COUpdateStartTimesEnsureName(os.Name)

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
				Nested:       err,
				UpdateEffect: payload.UpdateEffectNone,
				Reason:       "ClusterOperatorNotAvailable",
				Message:      fmt.Sprintf("Cluster operator %s has not yet reported success", expected.Name),
				Name:         expected.Name,
			}
			return false, nil
		}

		if len(expected.Status.Versions) == 0 {
			lastErr = &payload.UpdateError{
				UpdateEffect: payload.UpdateEffectFail,
				Reason:       "ClusterOperatorNotAvailable",
				Message:      fmt.Sprintf("Cluster operator %s does not declare expected versions", expected.Name),
				Name:         expected.Name,
			}
			return false, nil
		}

		// undone is a sorted slice of transition messages for incomplete operands.
		undone := make([]string, 0, len(expected.Status.Versions))
		for _, expOp := range expected.Status.Versions {
			current := ""
			for _, actOp := range actual.Status.Versions {
				if actOp.Name == expOp.Name {
					current = actOp.Version
					break
				}
			}
			if current != expOp.Version {
				if current == "" {
					undone = append(undone, fmt.Sprintf("%s to %s", expOp.Name, expOp.Version))
				} else {
					undone = append(undone, fmt.Sprintf("%s from %s to %s", expOp.Name, current, expOp.Version))
				}
			}
		}
		sort.Strings(undone)

		available := false
		var availableCondition *configv1.ClusterOperatorStatusCondition
		progressing := true
		degraded := true
		var degradedCondition *configv1.ClusterOperatorStatusCondition
		for i := range actual.Status.Conditions {
			condition := &actual.Status.Conditions[i]
			switch {
			case condition.Type == configv1.OperatorAvailable:
				if condition.Status == configv1.ConditionTrue {
					available = true
				}
				availableCondition = condition
			case condition.Type == configv1.OperatorProgressing && condition.Status == configv1.ConditionFalse:
				progressing = false
			case condition.Type == configv1.OperatorDegraded:
				if condition.Status == configv1.ConditionFalse {
					degraded = false
				}
				degradedCondition = condition
			}
		}

		nestedMessage := fmt.Errorf("cluster operator %s: available=%v, progressing=%v, degraded=%v, undone=%s",
			actual.Name, available, progressing, degraded, strings.Join(undone, ", "))

		if !available {
			if availableCondition != nil && len(availableCondition.Message) > 0 {
				nestedMessage = fmt.Errorf("cluster operator %s is %s=%s: %s: %s", actual.Name, availableCondition.Type, availableCondition.Status, availableCondition.Reason, availableCondition.Message)
			}
			lastErr = &payload.UpdateError{
				Nested:       nestedMessage,
				UpdateEffect: payload.UpdateEffectFail,
				Reason:       "ClusterOperatorNotAvailable",
				Message:      fmt.Sprintf("Cluster operator %s is not available", actual.Name),
				Name:         actual.Name,
			}
			return false, nil
		}

		// during initialization we allow degraded
		if degraded && mode != resourcebuilder.InitializingMode {
			if degradedCondition != nil && len(degradedCondition.Message) > 0 {
				nestedMessage = fmt.Errorf("cluster operator %s is %s=%s: %s, %s", actual.Name, degradedCondition.Type, degradedCondition.Status, degradedCondition.Reason, degradedCondition.Message)
			}
			lastErr = &payload.UpdateError{
				Nested:       nestedMessage,
				UpdateEffect: payload.UpdateEffectFailAfterInterval,
				Reason:       "ClusterOperatorDegraded",
				Message:      fmt.Sprintf("Cluster operator %s is degraded", actual.Name),
				Name:         actual.Name,
			}
			return false, nil
		}

		// during initialization we allow undone versions
		if len(undone) > 0 && mode != resourcebuilder.InitializingMode {
			nestedMessage = fmt.Errorf("cluster operator %s is available and not degraded but has not finished updating to target version", actual.Name)
			lastErr = &payload.UpdateError{
				Nested:       nestedMessage,
				UpdateEffect: payload.UpdateEffectNone,
				Reason:       "ClusterOperatorUpdating",
				Message:      fmt.Sprintf("Cluster operator %s is updating versions", actual.Name),
				Name:         actual.Name,
			}
			return false, nil
		}

		return true, nil
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
