package resourceread

import (
	"testing"
)

func TestReadCustomResourceDefinitionOrDie(t *testing.T) {
	type args struct {
		objBytes []byte
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "v1",
			args: args{
				objBytes: []byte(`
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: shoulds.parse.com
spec:
  group: parse.com
  names:
    kind: ShouldParse 
    listKind: ShouldParseList
    plural: shoulds 
    singular: should 
  scope: Namespaced
  versions:
  - name: v1alpha1
    schema:
      openAPIV3Schema:
        description: ShouldParse is a v1 CRD that should be parsed. 
        type: object
    served: true
    storage: true
    subresources:
      status: {}
`),
			},
		},
		{
			name: "v1beta1",
			args: args{
				objBytes: []byte(`
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: alreadys.parse.com
spec:
  group: parse.com
  names:
    kind: AlreadyParse 
    listKind: AlreadyParseList
    plural: alreadys 
    singular: already 
  scope: Namespaced
  versions:
  - name: v1alpha1
    schema:
      openAPIV3Schema:
        description: AlreadyParse is a v1beta1 CRD that should be parsed. 
        type: object
    served: true
    storage: true
    subresources:
      status: {}
`),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Error(r)
					t.Fail()
				}
			}()
			_ = ReadCustomResourceDefinitionOrDie(tt.args.objBytes)
		})
	}
}
