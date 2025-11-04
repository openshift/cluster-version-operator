#!/usr/bin/env bash

set -eu

source hack/build-info.sh

# Update test metadata
if ! "${BIN_PATH}/cluster-version-operator-tests" "update"; then
  >&2 echo "Failed to update metadata. It is likely because an incompatible change is introduced, such as a test is renamed.
See https://github.com/openshift/cluster-version-operator/blob/main/cmd/cluster-version-operator-tests/README.md#test-names for details."
  exit 1
fi
