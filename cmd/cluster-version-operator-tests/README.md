# cluster-version-operator-tests

It integrates [openshift-tests-extension](https://github.com/openshift-eng/openshift-tests-extension) to 
cluster-version-operator which allows openshift components to contribute tests to openshift-tests' suites with
extension binaries.


## Run the tests locally

## Using the framework
```console
$ hack/build-go.sh
$ _output/<OS>/<ARCH>/cluster-version-operator-tests run-suite cluster-version-operator
```

## Using ginko-cli

After [installing-ginkgo](https://onsi.github.io/ginkgo/#installing-ginkgo):

```console
$ ginkgo ./test/...
```

The output looks nicer this way.


## Test Names

### Test labels

See [docs](https://github.com/openshift/origin/tree/main/test/extended#test-labels) for details.

### Ownership

* A `[Jira:"Component"]` tag in the test name (e.g., [Jira:"Cluster Version Operator"]) is preferred to claim the ownership of the test. The component comes from [the list of components of Jira Project OCPBUGS](https://issues.redhat.com/projects/OCPBUGS?selectedItem=com.atlassian.jira.jira-projects-plugin:components-page). See [here](https://github.com/openshift/origin/blob/6b584254d53cdd1b5cd6471b69cb7c22f3e28ecd/test/extended/apiserver/rollout.go#L36) for example.

* Or an entry in the [ci-test-mapping](https://github.com/openshift-eng/ci-test-mapping) repository, which maps tests to owning components. See [here](https://github.com/openshift-eng/ci-test-mapping/tree/main/pkg/components/clusterversionoperator) for example.

### Renaming

If a test is renamed, claim it with [specs.Walk](https://pkg.go.dev/github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests#ExtensionTestSpecs.Walk):

```go
specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
    if orig, ok := renamedTests[spec.Name]; ok {
		spec.OriginalName = orig
	}
})
```

### Deleting

If a test is deleted, claim it with [ext.IgnoreObsoleteTests](https://pkg.go.dev/github.com/openshift-eng/openshift-tests-extension/pkg/extension#Extension.IgnoreObsoleteTests)

```go
ext.IgnoreObsoleteTests(
"[sig-abc] My removed test",
// Add more removed test names below
)
```
