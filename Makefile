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

clean:
	rm -rf _output/
	rm -rf bin
.PHONY: clean

update-codegen:
	./hack/update-codegen.sh
.PHONY: update-codegen

verify-codegen:
	./hack/verify-codegen.sh
.PHONY: verify-codegen

verify: verify-codegen
.PHONY: verify
