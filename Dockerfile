FROM openshift/origin-release:golang-1.10
COPY . /go/src/github.com/openshift/cluster-version-operator
RUN cd /go/src/github.com/openshift/cluster-version-operator && \
    make

FROM centos:7
COPY --from=0 /go/src/github.com/openshift/cluster-version-operator/bin/cvo /usr/bin/
CMD /usr/bin/cvo
