all: build
.PHONY: all

build:
	hack/build-go.sh
.PHONY: build

gotestsum:
	which gotestsum || go install gotest.tools/gotestsum@latest
.PHONY: gotestsum

JUNIT_DIR := "_output"
PACKAGES := "./..."
ifeq ($(CI),true)
JUNIT_DIR := "$(ARTIFACT_DIR)"
# tests in github.com/openshift/cluster-version-operator/test/ will be executed separately in CI
PACKAGES := $(shell go list ./... | grep -v "github.com/openshift/cluster-version-operator/test/" | xargs)
endif

test: gotestsum
	gotestsum --junitfile="$(JUNIT_DIR)/junit.xml" --packages="$(PACKAGES)"
.PHONY: test

integration-test:
	# This runs against a real OpenShift cluster through the current KUBECONFIG context
	TEST_INTEGRATION=1 go test ./... -test.run=^TestIntegration
.PHONY: integration-test

update: build
	hack/update-test-metadata.sh
.PHONY: update

format:
	go fmt ./...
.PHONY: format

imports:
	which gci || go install -mod=mod github.com/daixiang0/gci@latest
	gci write --custom-order -s standard -s default -s "prefix(k8s.io)" -s "prefix(github.com/openshift)" -s localmodule --skip-vendor .
.PHONY: imports

verify: verify-yaml verify-update
.PHONY: verify

verify-yaml:
	hack/verify-yaml.sh
.PHONY: verify-yaml

verify-update: imports update
	git diff --exit-code HEAD
.PHONY: verify-update

clean:
	rm -rf _output/
.PHONY: clean
