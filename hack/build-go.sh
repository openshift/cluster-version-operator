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

echo "Building binaries into ${BIN_PATH}"
mkdir -p ${BIN_PATH}

# Build the cluster-version-operator-tests binary and compress it
OPENSHIFT_TESTS_EXTENSION_MODULE="github.com/openshift-eng/openshift-tests-extension"
GIT_COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_TREE_STATE=$(if git diff --quiet; then echo clean; else echo dirty; fi)
LDFLAGS_TEST_EXTENSION=$GLDFLAGS
LDFLAGS_TEST_EXTENSION+=" -X '${OPENSHIFT_TESTS_EXTENSION_MODULE}/pkg/version.CommitFromGit=${GIT_COMMIT}'"
LDFLAGS_TEST_EXTENSION+=" -X '${OPENSHIFT_TESTS_EXTENSION_MODULE}/pkg/version.BuildDate=${BUILD_DATE}'"
LDFLAGS_TEST_EXTENSION+=" -X '${OPENSHIFT_TESTS_EXTENSION_MODULE}/pkg/version.GitTreeState=${GIT_TREE_STATE}'"

echo "Building ${REPO} cluster-version-operator-tests binary (${VERSION_OVERRIDE})"
GO_COMPLIANCE_POLICY="exempt_all" CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH}   \
  go build                                                                      \
  ${GOFLAGS}                                                                    \
  -ldflags "${LDFLAGS_TEST_EXTENSION}"                                          \
  -o "${BIN_PATH}/cluster-version-operator-tests"                               \
  "${REPO}/cmd/cluster-version-operator-tests/..."

echo "Compressing the cluster-version-operator-tests binary"
gzip --keep --force "${BIN_PATH}/cluster-version-operator-tests"

# Build the cluster-version-operator binary
GLDFLAGS+="-X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"
echo "Building ${REPO} cluster-version-operator binary (${VERSION_OVERRIDE})"
GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS}" -o ${BIN_PATH}/cluster-version-operator ${REPO}/cmd/cluster-version-operator/...
