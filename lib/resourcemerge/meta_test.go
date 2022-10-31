package resourcemerge

import (
	"flag"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

func init() {
	klog.InitFlags(flag.CommandLine)
	_ = flag.CommandLine.Lookup("v").Value.Set("2")
	_ = flag.CommandLine.Lookup("alsologtostderr").Value.Set("true")
}

func TestMergeOwnerRefs(t *testing.T) {
	tests := []struct {
		existing []metav1.OwnerReference
		input    []metav1.OwnerReference

		expectedModified bool
		expected         []metav1.OwnerReference
	}{{
		existing: []metav1.OwnerReference{},
		input:    []metav1.OwnerReference{},

		expectedModified: false,
		expected:         []metav1.OwnerReference{},
	}, {
		existing: []metav1.OwnerReference{},
		input: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},

		expectedModified: true,
		expected: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},
	}, {
		existing: []metav1.OwnerReference{{
			UID: types.UID("uid-2"),
		}, {
			UID: types.UID("uid-3"),
		}},
		input: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},

		expectedModified: true,
		expected: []metav1.OwnerReference{{
			UID: types.UID("uid-2"),
		}, {
			UID: types.UID("uid-3"),
		}, {
			UID: types.UID("uid-1"),
		}},
	}, {
		existing: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},
		input: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},

		expectedModified: false,
		expected: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},
	}, {
		existing: []metav1.OwnerReference{{
			UID: types.UID("uid-1"),
		}},
		input: []metav1.OwnerReference{{
			Controller: pointer.Bool(true),
			UID:        types.UID("uid-1"),
		}},

		expectedModified: true,
		expected: []metav1.OwnerReference{{
			Controller: pointer.Bool(true),
			UID:        types.UID("uid-1"),
		}},
	}, {
		existing: []metav1.OwnerReference{{
			Controller: pointer.Bool(false),
			UID:        types.UID("uid-1"),
		}},
		input: []metav1.OwnerReference{{
			Controller: pointer.Bool(true),
			UID:        types.UID("uid-1"),
		}},

		expectedModified: true,
		expected: []metav1.OwnerReference{{
			Controller: pointer.Bool(true),
			UID:        types.UID("uid-1"),
		}},
	}}

	for idx, test := range tests {
		t.Run(fmt.Sprintf("test#%d", idx), func(t *testing.T) {
			modified := pointer.Bool(false)
			mergeOwnerRefs(modified, &test.existing, test.input)
			if *modified != test.expectedModified {
				t.Fatalf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Fatalf("mismatch ownerefs got: %v want: %v", test.existing, test.expected)
			}
		})
	}
}
