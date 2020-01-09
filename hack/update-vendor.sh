#!/usr/bin/env bash
#
# This script updates Go vendoring using dep.

set -euo pipefail

# Go to the root of the repo
cd "$(git rev-parse --show-cdup)"

# Run dep.
go mod vendor
go mod verify

(cd hack && ./update-codegen.sh)
