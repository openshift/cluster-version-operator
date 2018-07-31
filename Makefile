GLDFLAGS = \
    -X 'github.com/openshift/cluster-version-operator/pkg/version.Raw=$(VERSION)' \

VERSION = $(shell git describe --dirty || echo "was not built properly (no history)")

.PHONY: bin/cvo
bin/cvo:
	go build -ldflags "${GLDFLAGS}" -o $@ ./cmd
