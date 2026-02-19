all: build
.PHONY: all

build:
	hack/build-go.sh
.PHONY: build

gotestsum:
	which gotestsum || go install gotest.tools/gotestsum@latest
.PHONY: gotestsum

JUNIT_DIR := "_output"
# tests in github.com/openshift/cluster-version-operator/test/ are e2e tests executed separately
PACKAGES := $(shell go list ./... | grep -v "github.com/openshift/cluster-version-operator/test/" | xargs)
ifeq ($(CI),true)
JUNIT_DIR := "$(ARTIFACT_DIR)"
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

verify: verify-yaml verify-update
.PHONY: verify

verify-yaml:
	hack/verify-yaml.sh
.PHONY: verify-yaml

verify-update: update
	git diff --exit-code HEAD
.PHONY: verify-update

clean:
	rm -rf _output/
.PHONY: clean
