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
	./hack/integration-test.sh
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
