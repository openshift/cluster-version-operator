FROM registry.ci.openshift.org/openshift/release:golang-1.19 AS builder
WORKDIR /go/src/github.com/openshift/cluster-version-operator
COPY . .
RUN hack/build-go.sh; \
    mkdir -p /tmp/build; \
    cp _output/linux/$(go env GOARCH)/cluster-version-operator /tmp/build/cluster-version-operator

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
COPY --from=builder /tmp/build/cluster-version-operator /usr/bin/
COPY install /manifests
COPY vendor/github.com/openshift/api/config/v1/zz_generated.crd-manifests/0000_00_cluster-version-operator_* /manifests/
COPY vendor/github.com/openshift/api/operator/v1alpha1/zz_generated.crd-manifests/0000_00_cluster-version-operator_* /manifests/
COPY bootstrap /bootstrap
ENTRYPOINT ["/usr/bin/cluster-version-operator"]
