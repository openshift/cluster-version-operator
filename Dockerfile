FROM registry.ci.openshift.org/openshift/release:golang-1.18 AS builder
WORKDIR /go/src/github.com/openshift/cluster-version-operator
COPY . .
RUN hack/build-go.sh; \
    mkdir -p /tmp/build; \
    cp _output/linux/$(go env GOARCH)/cluster-version-operator /tmp/build/cluster-version-operator

FROM registry.ci.openshift.org/ocp/ubi:8
COPY --from=builder /tmp/build/cluster-version-operator /usr/bin/
COPY install /manifests
COPY vendor/github.com/openshift/api/config/v1/0000_00_cluster-version-operator_01_clusterversion.crd.yaml /manifests/
COPY vendor/github.com/openshift/api/config/v1/0000_00_cluster-version-operator_01_clusteroperator.crd.yaml /manifests/
COPY vendor/github.com/openshift/api/operator/v1/0000_50_cluster_storage_operator_01_crd.yaml /manifests/
COPY bootstrap /bootstrap
ENTRYPOINT ["/usr/bin/cluster-version-operator"]
