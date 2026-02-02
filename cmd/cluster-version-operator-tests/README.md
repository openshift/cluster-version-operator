# cluster-version-operator-tests

It integrates [openshift-tests-extension](https://github.com/openshift-eng/openshift-tests-extension) to 
cluster-version-operator which allows openshift components to contribute tests to openshift-tests' suites with
extension binaries.

## Build the executable binary
In the root folder, run the following command to build the executable binary:
```console
$ make build
```

## Run the tests locally

### Using the binary
- run a test-suite
```console
$ _output/<OS>/<ARCH>/cluster-version-operator-tests run-suite <test suite name>
```
where test suites can be listed by `_output/<OS>/<ARCH>/cluster-version-operator-tests info`.

- run a single test case
```console
$ _output/<OS>/<ARCH>/cluster-version-operator-tests run-test <test case name>
```
where test names can be listed by `_output/<OS>/<ARCH>/cluster-version-operator-tests list`.

### Using ginkgo-cli

After [installing-ginkgo](https://onsi.github.io/ginkgo/#installing-ginkgo):

```console
$ ginkgo ./test/...
```
or run a specific test via `--focus` provided by [ginkgo-cli](https://onsi.github.io/ginkgo/#description-based-filtering).

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

## Run together with payload testing

By joining CVO's test suite `openshift/cluster-version-operator/conformance/parallel` and `openshift/cluster-version-operator/conformance/serial` as children [here](https://github.com/openshift/cluster-version-operator/blob/f25054a5800a34e3fd596b5d9a2c6c1bb5f5f628/cmd/cluster-version-operator-tests/main.go#L23-L38) and register them as extension [here](https://github.com/openshift/origin/blob/e3f443af6b2fd61a33ed8d060eb9083333ef426b/pkg/test/extensions/binary.go#L258-L261), the e2e testcases developed here in this repo will be executed in more tests than the ones defined in Presubmits for CVO's CI. For example, they will be running with [payload testing](https://docs.ci.openshift.org/docs/release-oversight/payload-testing/).
In order to avoid breaking the OpenShift release, it is suggested to start with skipping new testcases on MicroShift, HyperShift unless they are verified. We may add them in as needed later. There are [utility functions](https://github.com/openshift/cluster-version-operator/blob/f25054a5800a34e3fd596b5d9a2c6c1bb5f5f628/test/cvo/cvo.go#L62-L66) that help determine the cluster type.
