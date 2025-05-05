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

format:
	go fmt ./...
.PHONY: format

verify: verify-yaml
.PHONY: verify

verify-yaml:
	hack/verify-yaml.sh
.PHONY: verify-yaml

clean:
	rm -rf _output/
	rm -rf bin
.PHONY: clean
