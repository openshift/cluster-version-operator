#!/usr/bin/env bash

set -euo pipefail

oc patch FeatureGate cluster --type='json' -p '[{"op": "replace", "path": "/spec/featureSet", "value":"DevPreviewNoUpgrade"}]'
oc wait --for create ns/openshift-update-status-controller --timeout=120s
oc -n openshift-update-status-controller wait --for create deployment/update-status-controller --timeout=30s
oc create cm -n openshift-update-status-controller status-api-cm-prototype
oc -n openshift-update-status-controller wait --for create cm/status-api-cm-prototype --timeout=10s
sleep 120
oc get cm -n openshift-update-status-controller status-api-cm-prototype -o yaml > ${ARTIFACT_DIR}/status-api-cm-prototype.yaml
