FROM golang:1.10.3 AS build-env

COPY . /go/src/github.com/openshift/cluster-version-operator
WORKDIR /go/src/github.com/openshift/cluster-version-operator
RUN ./hack/build-go.sh

# Using alpine instead of scratch because the Job
# used to extract updatepayload from CVO image uses
# cp command.
FROM alpine
COPY --from=build-env /go/src/github.com/openshift/cluster-version-operator/_output/linux/amd64/cluster-version-operator /bin/cluster-version-operator
COPY install /manifests
COPY bootstrap /bootstrap

ENTRYPOINT ["/bin/cluster-version-operator"]
