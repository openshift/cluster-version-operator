#!/usr/bin/env bash

set -eu

# Go to the root of the repo
cd "$(git rev-parse --show-cdup)"

# Source build variables
source hack/build-info.sh

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
TAGS_FLAG=""
if [ -n "${TAGS:-}" ]; then
  TAGS_FLAG="-tags=${TAGS}"
fi
GO_COMPLIANCE_POLICY="exempt_all" CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH}   \
  go build                                                                      \
  ${GOFLAGS}                                                                    \
  ${TAGS_FLAG}                                                                  \
  -ldflags "${LDFLAGS_TEST_EXTENSION}"                                          \
  -o "${BIN_PATH}/cluster-version-operator-tests"                               \
  "${REPO}/cmd/cluster-version-operator-tests/..."

echo "Compressing the cluster-version-operator-tests binary"
gzip --keep --force "${BIN_PATH}/cluster-version-operator-tests"

# Build the cluster-version-operator binary
GLDFLAGS+="-X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"
echo "Building ${REPO} cluster-version-operator binary (${VERSION_OVERRIDE})"
GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} ${TAGS_FLAG} -ldflags "${GLDFLAGS}" -o ${BIN_PATH}/cluster-version-operator ${REPO}/cmd/cluster-version-operator/...
