FROM registry.svc.ci.openshift.org/ocp/builder:golang-1.10 AS builder
WORKDIR /go/src/github.com/openshift/cluster-version-operator
COPY . .
RUN hack/build-go.sh; \
    mkdir -p /tmp/build; \
    cp _output/linux/$(go env GOARCH)/cluster-version-operator /tmp/build/cluster-version-operator

FROM registry.svc.ci.openshift.org/ocp/4.0:base
COPY --from=builder /tmp/build/cluster-version-operator /usr/bin/
COPY install /manifests
COPY bootstrap /bootstrap
ENTRYPOINT ["/usr/bin/cluster-version-operator"]
