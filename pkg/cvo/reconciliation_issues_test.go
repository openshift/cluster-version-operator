package cvo

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/manifest"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

func Test_reconciliationIssueFromError(t *testing.T) {
	err := summarizeTaskGraphErrors([]error{
		&payload.UpdateError{
			Name:                "etcd",
			UpdateEffect:        payload.UpdateEffectNone,
			Reason:              "ClusterOperatorUpdating",
			PluralReason:        "ClusterOperatorsUpdating",
			Message:             "Cluster operator etcd is updating versions",
			PluralMessageFormat: "Cluster operators %s are updating versions",
			Nested:              errors.New("cluster operator etcd is available and not degraded but has not finished updating to target version"),
			Task: &payload.Task{
				Index: 2,
				Total: 10,
				Manifest: &manifest.Manifest{
					OriginalFilename: "etcd-cluster-operator.yaml",
					GVK:              configv1.GroupVersion.WithKind("ClusterOperator"),
				},
			},
		},
		&payload.UpdateError{
			Name:                "kube-apiserver",
			UpdateEffect:        payload.UpdateEffectNone,
			Reason:              "ClusterOperatorUpdating",
			PluralReason:        "ClusterOperatorsUpdating",
			Message:             "Cluster operator kube-apiserver is updating versions",
			PluralMessageFormat: "Cluster operators %s are updating versions",
			Nested:              errors.New("cluster operator kube-apiserver is available and not degraded but has not finished updating to target version"),
			Task: &payload.Task{
				Index: 4,
				Total: 10,
				Manifest: &manifest.Manifest{
					OriginalFilename: "kube-apiserver-cluster-operator.yaml",
					GVK:              configv1.GroupVersion.WithKind("ClusterOperator"),
				},
			},
		},
	})

	message, err := reconciliationIssueFromError(err)
	if err != nil {
		t.Fatal(err)
	}

	var data interface{}
	if err := json.Unmarshal([]byte(message), &data); err != nil {
		t.Fatal(err)
	}

	indentedMessage, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	expected := `{
  "children": [
    {
      "children": [
        {
          "children": [
            {
              "message": "cluster operator etcd is available and not degraded but has not finished updating to target version"
            }
          ],
          "effect": "None",
          "manifest": {
            "group": "config.openshift.io",
            "kind": "ClusterOperator",
            "name": "",
            "originalFilename": "etcd-cluster-operator.yaml"
          },
          "message": "Cluster operator etcd is updating versions"
        },
        {
          "children": [
            {
              "message": "cluster operator kube-apiserver is available and not degraded but has not finished updating to target version"
            }
          ],
          "effect": "None",
          "manifest": {
            "group": "config.openshift.io",
            "kind": "ClusterOperator",
            "name": "",
            "originalFilename": "kube-apiserver-cluster-operator.yaml"
          },
          "message": "Cluster operator kube-apiserver is updating versions"
        }
      ],
      "message": "[Cluster operator etcd is updating versions, Cluster operator kube-apiserver is updating versions]"
    }
  ],
  "effect": "None",
  "message": "Cluster operators etcd, kube-apiserver are updating versions"
}`

	diff := cmp.Diff(strings.Split(expected, "\n"), strings.Split(string(indentedMessage), "\n"))
	if diff != "" {
		t.Fatalf("unexpected output (-want, +got):\n%s", diff)
	}
}
