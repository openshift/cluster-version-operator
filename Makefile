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

CRD_SCHEMA_GEN_VERSION := v1.0.0
crd-schema-gen:
	git clone -b $(CRD_SCHEMA_GEN_VERSION) --single-branch --depth 1 https://github.com/openshift/crd-schema-gen.git $(CRD_SCHEMA_GEN_GOPATH)/src/github.com/openshift/crd-schema-gen
	GOPATH=$(CRD_SCHEMA_GEN_GOPATH) GOBIN=$(CRD_SCHEMA_GEN_GOPATH)/bin go install $(CRD_SCHEMA_GEN_GOPATH)/src/github.com/openshift/crd-schema-gen/cmd/crd-schema-gen
.PHONY: crd-schema-gen

update-codegen-crds: CRD_SCHEMA_GEN_GOPATH :=$(shell mktemp -d)
update-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_GOPATH)/bin/crd-schema-gen --apis-dir vendor/github.com/openshift/api/config/v1 --manifests-dir install
.PHONY: update-codegen-crds
update-codegen: update-codegen-crds
.PHONY: update-codegen

verify-codegen-crds: CRD_SCHEMA_GEN_GOPATH :=$(shell mktemp -d)
verify-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_GOPATH)/bin/crd-schema-gen --apis-dir vendor/github.com/openshift/api/config/v1 --verify-only --manifests-dir install
.PHONY: verify-codegen-crds
verify-codegen: verify-codegen-crds
.PHONY: verify-codegen

verify: verify-codegen
