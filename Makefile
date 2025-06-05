BIN_DIR?=./bin

all: build
.PHONY: all

build:
	hack/build-go.sh
.PHONY: build

test:
	go test ./...
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
	rm -rf bin
.PHONY: clean
