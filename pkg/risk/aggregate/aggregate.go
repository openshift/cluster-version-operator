// Package aggregate implements an update risk source that aggregates
// lower-level update risk sources.  package aggregate
package aggregate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/risk"
)

// aggregate implements an update risk source that aggregates
// lower-level update risk sources.
type aggregate struct {
	sources []risk.Source
}

func New(sources ...risk.Source) risk.Source {
	return &aggregate{sources: sources}
}

// Name returns the source's name.
func (a *aggregate) Name() string {
	names := make([]string, 0, len(a.sources))
	for _, source := range a.sources {
		names = append(names, source.Name())
	}
	return fmt.Sprintf("Aggregate(%s)", strings.Join(names, ", "))
}

// Risks returns the current set of risks the source is aware of.
func (a *aggregate) Risks(ctx context.Context, versions []string) (risks []configv1.ConditionalUpdateRisk, versionMap map[string][]string, err error) {
	var errs []error
	riskMap := make(map[string]configv1.ConditionalUpdateRisk)
	for _, source := range a.sources {
		r, v, err := source.Risks(ctx, versions)
		if err != nil {
			errs = append(errs, err) // collect grumbles, but continue to process anything we got back
		}
		for i, risk := range r {
			if existingRisk, ok := riskMap[risk.Name]; ok {
				if diff := cmp.Diff(existingRisk, risk, cmpopts.IgnoreFields(configv1.ConditionalUpdateRisk{}, "Conditions")); diff != "" {
					errs = append(errs, fmt.Errorf("divergent definitions of risk %q from %q:\n%s", risk.Name, source.Name(), diff))
				}
				continue
			}
			riskMap[risk.Name] = r[i]
			risks = append(risks, r[i])
		}
		if len(v) > 0 && versionMap == nil {
			versionMap = make(map[string][]string, len(v))
		}
		for riskName, vs := range v {
			if existingVersions, ok := versionMap[riskName]; ok {
				for _, version := range vs {
					found := false
					for _, existingVersion := range existingVersions {
						if version == existingVersion {
							found = true
							break
						}
					}
					if !found {
						existingVersions = append(existingVersions, version)
					}
				}
				vs = existingVersions
			}
			versionMap[riskName] = vs
		}
	}
	return risks, versionMap, errors.Join(errs...)
}
