FROM registry.svc.ci.openshift.org/openshift/release:golang-1.10 AS builder
WORKDIR /go/src/github.com/openshift/cluster-version-operator
COPY . .
RUN hack/build-go.sh

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/cluster-version-operator/_output/linux/amd64/cluster-version-operator /usr/bin/
COPY install /manifests
COPY vendor/github.com/openshift/api/config/v1/0000_00_cluster-version-operator_01_clusterversion.crd.yaml /manifests/
COPY vendor/github.com/openshift/api/config/v1/0000_00_cluster-version-operator_01_clusteroperator.crd.yaml /manifests/
COPY bootstrap /bootstrap
ENTRYPOINT ["/usr/bin/cluster-version-operator"]
