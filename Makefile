all: build
.PHONY: all

build:
	hack/build-go.sh
.PHONY: build

test:
	go test ./...
.PHONY: test

clean:
	rm -rf _output/
.PHONY: clean

update-codegen-crds:
	go run ./vendor/github.com/openshift/library-go/cmd/crd-schema-gen/main.go --domain openshift.io --apis-dir vendor/github.com/openshift/api --manifests-dir install/
update-codegen: update-codegen-crds
verify-codegen-crds:
	go run ./vendor/github.com/openshift/library-go/cmd/crd-schema-gen/main.go --domain openshift.io --apis-dir vendor/github.com/openshift/api --manifests-dir install/ --verify-only
verify-codegen: verify-codegen-crds
verify: verify-codegen
.PHONY: update-codegen-crds update-codegen verify-codegen-crds verify-codegen verify

# TODO: Add install step for golangci-lint version 1.24.0
# TODO: Once we fix the resonable amount of lint issues, we need to make it default in the unit test
.PHONY: lint
lint:
	golangci-lint run -c ./golangci.yml ./pkg/... ./cmd/... ./lib/...
