FROM golang:1.10.3 AS build-env

COPY . /go/src/github.com/openshift/cluster-version-operator
WORKDIR /go/src/github.com/openshift/cluster-version-operator
RUN ./hack/build-go.sh

FROM scratch
COPY --from=build-env /go/src/github.com/openshift/cluster-version-operator/_output/linux/amd64/cluster-version-operator /bin/cluster-version-operator

ENTRYPOINT ["/bin/cluster-version-operator"]
