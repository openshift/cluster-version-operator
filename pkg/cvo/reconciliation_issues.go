package cvo

import (
	"encoding/json"
	"fmt"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

const (
	reconciliationIssuesConditionType configv1.ClusterStatusConditionType = "ReconciliationIssues"

	noReconciliationIssuesReason  string = "NoIssues"
	noReconciliationIssuesMessage string = "No issues found during reconciliation"

	reconciliationIssuesFoundReason string = "IssuesFound"
)

// errorWalkCallback processes an error.  It returns an error to fail the walk.
type errorWalkCallback func(err error, depth int) error

// errorWalk walks an error depth-first via Unwrap(), and calls the
// callback on each error until the callback returns false for continuing
// or an error.
func errorWalk(err error, depth int, fn errorWalkCallback) error {
	if err == nil {
		return nil
	}

	if err2 := fn(err, depth); err2 != nil {
		return err2
	}

	switch errType := err.(type) {
	case interface{ Unwrap() error }:
		return errorWalk(errType.Unwrap(), depth+1, fn)
	case interface{ Unwrap() []error }:
		for _, err := range errType.Unwrap() {
			err := errorWalk(err, depth+1, fn)
			if err != nil {
				return err
			}
		}
		return nil
	case interface{ Errors() []error }: // k8s.io/apimachinery/pkg/util/errors hasn't caught up with Unwrap() []error
		for _, err := range errType.Errors() {
			err := errorWalk(err, depth+1, fn)
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

type reconciliationIssue struct {
	Message  string                 `json:"message"`
	Children []*reconciliationIssue `json:"children,omitempty"`

	// The following properties export portions of payload.UpdateError

	Effect   string              `json:"effect,omitempty"`
	Manifest *reconciledManifest `json:"manifest,omitempty"`
}

type reconciledManifest struct {
	OriginalFilename string `json:"originalFilename,omitempty"`
	Group            string `json:"group"`
	Kind             string `json:"kind"`
	Namespace        string `json:"namespace,omitempty"`
	Name             string `json:"name"`
}

func reconciliationIssueFromError(err error) (string, error) {
	root := &reconciliationIssue{}

	if err := errorWalk(err, 0, func(err error, depth int) error {
		parent := root
		var entry *reconciliationIssue
		if depth == 0 {
			entry = root
		} else {
			for i := 0; i < depth-1; i++ {
				parent = parent.Children[len(parent.Children)-1]
			}
			entry = &reconciliationIssue{}
			parent.Children = append(parent.Children, entry)
		}

		entry.Message = err.Error()
		if updateErr, ok := err.(*payload.UpdateError); ok {
			entry.Effect = string(updateErr.UpdateEffect)
			if updateErr.Task != nil && updateErr.Task.Manifest != nil {
				entry.Manifest = &reconciledManifest{
					OriginalFilename: updateErr.Task.Manifest.OriginalFilename,
					Group:            updateErr.Task.Manifest.GVK.Group,
					Kind:             updateErr.Task.Manifest.GVK.Kind,
				}
				if updateErr.Task.Manifest.Obj != nil {
					entry.Manifest.Namespace = updateErr.Task.Manifest.Obj.GetNamespace()
					entry.Manifest.Name = updateErr.Task.Manifest.Obj.GetName()
				}
			}
		}

		sort.Slice(parent.Children, func(i, j int) bool {
			if parent.Children[i].Manifest == nil && parent.Children[j].Manifest == nil {
				return parent.Children[i].Message < parent.Children[j].Message
			} else if parent.Children[i].Manifest == nil {
				return true
			} else if parent.Children[j].Manifest == nil {
				return false
			}
			return parent.Children[i].Manifest.OriginalFilename < parent.Children[j].Manifest.OriginalFilename
		})

		return nil
	}); err != nil {
		return "", err
	}

	bytes, err := json.Marshal(root)
	if err != nil {
		return string(bytes), err
	}

	if len(bytes) > 32768 {
		root.Children = []*reconciliationIssue{{
			Message: fmt.Sprintf("truncated children due to overly long JSON: %d bytes > 32768", len(bytes)),
		}}
		bytes, err = json.Marshal(root)
	}

	return string(bytes), err
}
