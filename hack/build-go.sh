#!/usr/bin/env bash

set -eu

REPO=github.com/openshift/cluster-version-operator
GOFLAGS=${GOFLAGS:--mod=vendor}
GLDFLAGS=${GLDFLAGS:-}

eval $(go env | grep -e "GOHOSTOS" -e "GOHOSTARCH")

GOOS=${GOOS:-${GOHOSTOS}}
GOARCH=${GOARCH:-${GOHOSTARCH}}

# Go to the root of the repo
cd "$(git rev-parse --show-cdup)"

VERSION_OVERRIDE=${VERSION_OVERRIDE:-${OS_GIT_VERSION:-}}
if [ -z "${VERSION_OVERRIDE:-}" ]; then
	echo "Using version from git..."
	VERSION_OVERRIDE=$(git describe --abbrev=8 --dirty --always)
fi

eval $(go env)

if [ -z ${BIN_PATH+a} ]; then
	export BIN_PATH=_output/${GOOS}/${GOARCH}
fi

mkdir -p ${BIN_PATH}

# Build the openshift-tests-extension and compress it
echo "Building ${REPO} openshift-tests-extension binary (${VERSION_OVERRIDE})"
GO_COMPLIANCE_POLICY="exempt_all" CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} \
go build                                                                      \
${GOFLAGS}                                                                    \
-ldflags "${GLDFLAGS}"                                                        \
-o "${BIN_PATH}/openshift-tests-extension"                                    \
"${REPO}/cmd/openshift-tests-extension/..."

echo "Compressing the openshift-tests-extension binary"
gzip --keep --force "${BIN_PATH}/openshift-tests-extension"

# Build the cluster-version-operator binary
GLDFLAGS+="-X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"
echo "Building ${REPO} cluster-version-operator binary (${VERSION_OVERRIDE})"
GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS}" -o ${BIN_PATH}/cluster-version-operator ${REPO}/cmd/cluster-version-operator/...
