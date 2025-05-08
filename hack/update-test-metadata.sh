#!/usr/bin/env bash

set -eu

source hack/build-info.sh

# Update test metadata
eval "${BIN_PATH}/cluster-version-operator-tests" "update"
