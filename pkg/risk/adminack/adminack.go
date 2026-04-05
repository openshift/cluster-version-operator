// Package adminack implements an update risk source based on
// administrator acknowledgements.
package adminack

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	informerscorev1 "k8s.io/client-go/informers/core/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/risk"
	"github.com/openshift/cluster-version-operator/pkg/risk/upgradeable"
)

const (
	adminAckGateFmt string = "^ack-([4-5][.][0-9]{1,})-[^-]"
)

var adminAckGateRegexp = regexp.MustCompile(adminAckGateFmt)

type adminAck struct {
	name             string
	currentVersion   func() configv1.Release
	adminGatesLister listerscorev1.ConfigMapNamespaceLister
	adminAcksLister  listerscorev1.ConfigMapNamespaceLister
	lastSeen         []configv1.ConditionalUpdateRisk
}

// New returns a new update-risk source, tracking administrator acknowledgements.
func New(name string, currentVersion func() configv1.Release, adminGatesInformer, adminAcksInformer informerscorev1.ConfigMapInformer, changeCallback func()) risk.Source {
	adminGatesLister := adminGatesInformer.Lister().ConfigMaps(internal.ConfigManagedNamespace)
	adminAcksLister := adminAcksInformer.Lister().ConfigMaps(internal.ConfigNamespace)
	source := &adminAck{name: name, currentVersion: currentVersion, adminGatesLister: adminGatesLister, adminAcksLister: adminAcksLister}
	adminGatesInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { source.eventHandler(changeCallback) },
		UpdateFunc: func(_, _ interface{}) { source.eventHandler(changeCallback) },
		DeleteFunc: func(_ interface{}) { source.eventHandler(changeCallback) },
	})
	adminAcksInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { source.eventHandler(changeCallback) },
		UpdateFunc: func(_, _ interface{}) { source.eventHandler(changeCallback) },
		DeleteFunc: func(_ interface{}) { source.eventHandler(changeCallback) },
	})
	return source
}

// Name returns the source's name.
func (a *adminAck) Name() string {
	return a.name
}

// Risks returns the current set of risks the source is aware of.
func (a *adminAck) Risks(_ context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	risks, version, err := a.risks()
	if meta.IsNoMatchError(err) {
		a.lastSeen = nil
		return nil, nil, nil
	}

	if len(risks) == 0 {
		a.lastSeen = nil
		return nil, nil, err
	}

	a.lastSeen = risks
	majorAndMinorUpdates := upgradeable.MajorAndMinorUpdates(version, versions)
	if len(majorAndMinorUpdates) == 0 {
		return risks, nil, err
	}

	versionMap := make(map[string][]string, len(risks))
	for _, risk := range risks {
		versionMap[risk.Name] = majorAndMinorUpdates
	}

	return risks, versionMap, err
}

func (a *adminAck) risks() ([]configv1.ConditionalUpdateRisk, semver.Version, error) {
	currentRelease := a.currentVersion()
	version, err := semver.Parse(currentRelease.Version)
	if err != nil {
		return []configv1.ConditionalUpdateRisk{{
			Name:          fmt.Sprintf("%sUnknown", a.name),
			Message:       fmt.Sprintf("Failed to parse cluster version to check admin-acks: %v", err),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}}, version, err
	}

	var gateCm *corev1.ConfigMap
	if gateCm, err = a.adminGatesLister.Get(internal.AdminGatesConfigMap); err != nil {
		return []configv1.ConditionalUpdateRisk{{
			Name:          fmt.Sprintf("%sUnknown", a.name),
			Message:       fmt.Sprintf("Failed to retrieve ConfigMap %s from namespace %s: %v", internal.AdminGatesConfigMap, internal.ConfigManagedNamespace, err),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}}, version, err
	}
	var ackCm *corev1.ConfigMap
	if ackCm, err = a.adminAcksLister.Get(internal.AdminAcksConfigMap); err != nil {
		return []configv1.ConditionalUpdateRisk{{
			Name:          fmt.Sprintf("%sUnknown", a.name),
			Message:       fmt.Sprintf("Failed to retrieve ConfigMap %s from namespace %s: %v", internal.AdminAcksConfigMap, internal.ConfigNamespace, err),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}}, version, err
	}
	var risks []configv1.ConditionalUpdateRisk
	for k, v := range gateCm.Data {
		if risk := checkAdminGate(k, v, fmt.Sprintf("%s-", a.name), version, ackCm); risk != nil {
			risks = append(risks, *risk)
		}
	}

	slices.SortFunc(risks, func(a, b configv1.ConditionalUpdateRisk) int {
		return strings.Compare(a.Name, b.Name)
	})

	return risks, version, err
}

func (a *adminAck) eventHandler(changeCallback func()) {
	risks, _, err := a.risks()
	if err != nil {
		if changeCallback != nil {
			changeCallback()
		}
		return
	}
	if diff := cmp.Diff(a.lastSeen, risks); diff != "" {
		klog.V(2).Infof("admin-ack risks changed (-old +new):\n%s", diff)
		if changeCallback != nil {
			changeCallback()
		}
	}
}

func gateApplicableToCurrentVersion(gateName string, currentVersion semver.Version) (bool, error) {
	var applicable bool
	if matches := adminAckGateRegexp.FindStringSubmatch(gateName); len(matches) != 2 {
		return false, fmt.Errorf("%s configmap gate name %s has invalid format; must comply with %q",
			internal.AdminGatesConfigMap, gateName, adminAckGateFmt)
	} else {
		ackVersion, err := semver.Parse(fmt.Sprintf("%s.0", matches[1]))
		if err != nil {
			return false, fmt.Errorf("failed to parse SemVer ack version from admin-gate %q: %w", gateName, err)
		}
		if ackVersion.Major == currentVersion.Major && ackVersion.Minor == currentVersion.Minor {
			applicable = true
		}
	}
	return applicable, nil
}

func checkAdminGate(gateName string, gateValue string, riskNamePrefix string, currentVersion semver.Version, ackConfigmap *corev1.ConfigMap) *configv1.ConditionalUpdateRisk {
	riskName := fmt.Sprintf("%s%s", riskNamePrefix, gateName)
	if applies, err := gateApplicableToCurrentVersion(gateName, currentVersion); err == nil {
		if !applies {
			return nil
		}
	} else {
		return &configv1.ConditionalUpdateRisk{
			Name:          riskName,
			Message:       fmt.Sprintf("%s configmap gate %q is invalid: %v", internal.AdminGatesConfigMap, gateName, err),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}

	}
	if gateValue == "" {
		return &configv1.ConditionalUpdateRisk{
			Name:          riskName,
			Message:       fmt.Sprintf("%s configmap gate %s must contain a non-empty value.", internal.AdminGatesConfigMap, gateName),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}
	}
	if val, ok := ackConfigmap.Data[gateName]; !ok || val != "true" {
		return &configv1.ConditionalUpdateRisk{
			Name:          riskName,
			Message:       gateValue,
			URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}
	}
	return nil
}
