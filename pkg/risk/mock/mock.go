// Package mock implements an update risk source with mock responses,
// for convenient testing.
package mock

import (
	"context"
	"errors"

	configv1 "github.com/openshift/api/config/v1"
)

// Mock implements an update risk source with mock responses.
type Mock struct {
	// InternalName is the source name.
	InternalName string

	// InternalRisks are the detected update risks.
	InternalRisks []configv1.ConditionalUpdateRisk

	// InternalVersions maps the detected risks to version
	// strings.  If unset, all internal risks will be reported as
	// applicable to all known versions.
	InternalVersions map[string][]string
}

// Name returns the source's name.
func (m *Mock) Name() string {
	return m.InternalName
}

// Risks returns the current set of risks the source is aware of.
func (m *Mock) Risks(_ context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	if m.InternalRisks == nil {
		return nil, nil, errors.New("failed to calculate risks")
	}
	var versionMap map[string][]string
	if m.InternalVersions != nil {
		versionMap = m.InternalVersions
	} else {
		versionMap = make(map[string][]string, len(m.InternalRisks))
		for _, risk := range m.InternalRisks {
			versionMap[risk.Name] = versions
		}
	}
	return m.InternalRisks, versionMap, nil
}
