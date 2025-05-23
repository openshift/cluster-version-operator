#!/usr/bin/env bash

set -eu

REPO=github.com/openshift/cluster-version-operator
GOFLAGS=${GOFLAGS:--mod=vendor}
GLDFLAGS=${GLDFLAGS:-}

eval $(go env | grep -e "GOHOSTOS" -e "GOHOSTARCH")

GOOS=${GOOS:-${GOHOSTOS}}
GOARCH=${GOARCH:-${GOHOSTARCH}}

VERSION_OVERRIDE=${VERSION_OVERRIDE:-${OS_GIT_VERSION:-}}
if [ -z "${VERSION_OVERRIDE:-}" ]; then
	echo "Using version from git..."
	VERSION_OVERRIDE=$(git describe --abbrev=8 --dirty --always)
fi

eval $(go env)

if [ -z ${BIN_PATH+a} ]; then
	export BIN_PATH=_output/${GOOS}/${GOARCH}
fi
