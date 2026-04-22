package readiness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	semver "github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

const (
	ApprovalAutomatic     = "Automatic"
	ApprovalManual        = "Manual"
	PhaseRequiresApproval = "RequiresApproval"
)

// OLMOperatorLifecycleCheck collects lifecycle information for OLM-installed operators
// by correlating Subscriptions, ClusterServiceVersions, InstallPlans, and PackageManifests.
// This data supports the Operator Update Planner (OCPSTRAT-2618) by providing per-operator
// installed version, OCP compatibility, update policy, pending upgrades, and channel info.
type OLMOperatorLifecycleCheck struct{}

func (c *OLMOperatorLifecycleCheck) Name() string { return "olm_operator_lifecycle" }

func (c *OLMOperatorLifecycleCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}
	var sectionErrors []map[string]any

	// Subscriptions are the anchor — fail hard if unavailable.
	subs, err := ListResources(ctx, dc, GVRSubscription, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list subscriptions: %w", err)
	}

	// Fetch CSVs and PackageManifests concurrently; both are independent.
	var (
		csvs         []unstructured.Unstructured
		pkgManifests []unstructured.Unstructured
		csvErr       error
		pkgErr       error
		fetchWG      sync.WaitGroup
	)
	fetchWG.Add(2)
	go func() {
		defer fetchWG.Done()
		csvs, csvErr = ListResources(ctx, dc, GVRCSV, "")
	}()
	go func() {
		defer fetchWG.Done()
		pkgManifests, pkgErr = ListResources(ctx, dc, GVRPackageManifest, "")
	}()
	fetchWG.Wait()

	if csvErr != nil {
		SectionError(&sectionErrors, "clusterserviceversions", csvErr)
	}
	if pkgErr != nil {
		SectionError(&sectionErrors, "packagemanifests", pkgErr)
	}

	csvIndex := indexByNamespacedName(csvs)
	pkgIndex := indexByName(pkgManifests)

	// Parse current/target once to avoid repeated semver parsing per operator.
	parsedTarget, errTarget := semver.ParseTolerant(target)
	parsedCurrent, errCurrent := semver.ParseTolerant(current)
	hasTarget := errTarget == nil && target != ""
	hasCurrent := errCurrent == nil && current != ""

	operators := make([]map[string]any, 0, len(subs))
	incompatibleWithTarget := 0
	pendingUpgradeCount := 0
	manualApprovalCount := 0

	for _, sub := range subs {
		entry := map[string]any{
			"name":      sub.GetName(),
			"namespace": sub.GetNamespace(),
		}

		entry["channel"] = NestedString(sub.Object, "spec", "channel")
		entry["source"] = NestedString(sub.Object, "spec", "source")
		entry["source_namespace"] = NestedString(sub.Object, "spec", "sourceNamespace")
		entry["package"] = NestedString(sub.Object, "spec", "name")

		approval := NestedString(sub.Object, "spec", "installPlanApproval")
		if approval == "" {
			approval = ApprovalAutomatic
		}
		entry["install_plan_approval"] = approval
		if approval == ApprovalManual {
			manualApprovalCount++
		}

		entry["state"] = NestedString(sub.Object, "status", "state")
		installedCSVName := NestedString(sub.Object, "status", "installedCSV")
		entry["installed_csv"] = installedCSVName
		currentCSVName := NestedString(sub.Object, "status", "currentCSV")

		if installedCSVName != "" {
			csvKey := sub.GetNamespace() + "/" + installedCSVName
			if csvObj, ok := csvIndex[csvKey]; ok {
				entry["installed_version"] = NestedString(csvObj, "spec", "version")
				entry["csv_phase"] = NestedString(csvObj, "status", "phase")
				entry["csv_display_name"] = NestedString(csvObj, "spec", "displayName")

				minKube := NestedString(csvObj, "spec", "minKubeVersion")
				if minKube != "" {
					entry["min_kube_version"] = minKube
				}
			}
		}

		pendingUpgrade := false
		if currentCSVName != "" && installedCSVName != "" && currentCSVName != installedCSVName {
			pendingUpgrade = true
			pendingUpgradeCount++
			entry["pending_csv"] = currentCSVName
			csvKey := sub.GetNamespace() + "/" + currentCSVName
			if csvObj, ok := csvIndex[csvKey]; ok {
				entry["pending_version"] = NestedString(csvObj, "spec", "version")
			}
		}
		entry["pending_upgrade"] = pendingUpgrade

		// Fetch the referenced InstallPlan directly instead of listing all.
		ipRef := NestedString(sub.Object, "status", "installPlanRef", "name")
		if ipRef != "" {
			ipObj, ipErr := GetNamespacedResource(ctx, dc, GVRInstallPlan, sub.GetNamespace(), ipRef)
			if ipErr == nil {
				ipApproved := NestedBool(ipObj.Object, "spec", "approved")
				ipPhase := NestedString(ipObj.Object, "status", "phase")
				if !ipApproved && ipPhase == PhaseRequiresApproval {
					entry["install_plan_awaiting_approval"] = true
				}
			}
		}

		pkgName := NestedString(sub.Object, "spec", "name")
		subChannel := NestedString(sub.Object, "spec", "channel")
		if pm, ok := pkgIndex[pkgName]; ok {
			compat := extractOCPCompat(pm, subChannel)
			if compat != nil {
				entry["ocp_compat"] = compat

				maxOCP, _ := compat["max"].(string)
				if maxOCP != "" && hasTarget {
					parsedMax, err := semver.ParseTolerant(maxOCP)
					if err == nil {
						if parsedTarget.Compare(parsedMax) > 0 {
							entry["compatible_with_target"] = false
							incompatibleWithTarget++
						} else {
							entry["compatible_with_target"] = true
						}
					}
				}
				minOCP, _ := compat["min"].(string)
				if minOCP != "" && hasCurrent {
					parsedMin, err := semver.ParseTolerant(minOCP)
					if err == nil {
						entry["compatible_with_current"] = parsedCurrent.Compare(parsedMin) >= 0
					}
				}
			}

			channels := extractChannels(pm)
			if len(channels) > 0 {
				entry["available_channels"] = channels
			}
		}

		operators = append(operators, entry)
	}

	result["operators"] = operators
	result["summary"] = map[string]any{
		"total_operators":          len(subs),
		"pending_upgrades":         pendingUpgradeCount,
		"manual_approval":          manualApprovalCount,
		"incompatible_with_target": incompatibleWithTarget,
	}

	if len(sectionErrors) > 0 {
		result["errors"] = sectionErrors
	}

	return result, nil
}

// indexByNamespacedName builds a lookup map keyed by "namespace/name".
func indexByNamespacedName(items []unstructured.Unstructured) map[string]map[string]interface{} {
	idx := make(map[string]map[string]interface{}, len(items))
	for _, item := range items {
		key := item.GetNamespace() + "/" + item.GetName()
		idx[key] = item.Object
	}
	return idx
}

// indexByName builds a lookup map keyed by name (for cluster-scoped resources).
func indexByName(items []unstructured.Unstructured) map[string]map[string]interface{} {
	idx := make(map[string]map[string]interface{}, len(items))
	for _, item := range items {
		idx[item.GetName()] = item.Object
	}
	return idx
}

// extractOCPCompat reads olm.maxOpenShiftVersion and olm.properties from a
// PackageManifest's channel entry to determine OCP version compatibility.
func extractOCPCompat(pm map[string]interface{}, channelName string) map[string]any {
	channels := NestedSlice(pm, "status", "channels")
	for _, ch := range channels {
		chMap, ok := ch.(map[string]interface{})
		if !ok {
			continue
		}
		if NestedString(chMap, "name") != channelName {
			continue
		}

		compat := map[string]any{}

		maxOCP := NestedString(chMap, "currentCSVDesc", "annotations", "olm.maxOpenShiftVersion")
		if maxOCP != "" {
			compat["max"] = maxOCP
		}

		props := NestedString(chMap, "currentCSVDesc", "annotations", "olm.properties")
		if props != "" {
			minOCP := parseMinOCPFromProperties(props)
			if minOCP != "" {
				compat["min"] = minOCP
			}
		}

		if len(compat) > 0 {
			return compat
		}
	}
	return nil
}

// olmProperty represents a single entry in the olm.properties JSON annotation.
type olmProperty struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// parseMinOCPFromProperties extracts the minimum OCP version from the olm.properties
// JSON annotation, which is a JSON array of {type, value} objects.
func parseMinOCPFromProperties(props string) string {
	var properties []olmProperty
	if err := json.Unmarshal([]byte(props), &properties); err != nil {
		return ""
	}
	for _, p := range properties {
		if p.Type == "olm.minOpenShiftVersion" {
			return p.Value
		}
	}
	return ""
}

// extractChannels returns the list of channel names from a PackageManifest.
func extractChannels(pm map[string]interface{}) []string {
	channels := NestedSlice(pm, "status", "channels")
	names := make([]string, 0, len(channels))
	for _, ch := range channels {
		chMap, ok := ch.(map[string]interface{})
		if !ok {
			continue
		}
		name := NestedString(chMap, "name")
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}
