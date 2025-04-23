#!/usr/bin/env bash

set -euo pipefail

yamllint -c hack/yamllint-config.yaml -s .
