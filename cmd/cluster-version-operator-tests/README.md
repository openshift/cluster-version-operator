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