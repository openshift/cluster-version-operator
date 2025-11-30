# AGENTS.md

This file provides guidance to AI code assistants when working with code in this repository.

## Overview

The Cluster Version Operator (CVO) is a core OpenShift Cluster Operator that manages cluster upgrades by reconciling release payload images to the cluster. It continuously monitors for available updates from the OpenShift Update Service (OSUS) and orchestrates the systematic rollout of new versions across all cluster operators.

## Building and Testing

### Build commands
```bash
# Build the CVO binary
make build                # Runs hack/build-go.sh, outputs to _output/

# Build the CVO container image
./hack/build-image.sh     # Creates local image with git version tag

# Push development image to a registry
REPO=quay.io/yourname ./hack/push-image.sh

# Format code
make format              # Run go fmt on all packages
```

### Testing commands
```bash
# Unit tests
make test               # Runs gotestsum with JUnit XML output to _output/junit.xml

# Single package test
gotestsum --packages="./pkg/cvo"

# Integration tests (requires KUBECONFIG with admin credentials for a disposable cluster)
make integration-test   # Runs tests with TEST_INTEGRATION=1 against real cluster

# Update test metadata (for tests in test/ directory)
make update

# Verification
make verify             # Runs verify-yaml and verify-update
make verify-yaml        # Validates YAML manifests
make verify-update      # Ensures generated files are up-to-date
```

## Architecture

### Core Components

#### Main Operator Loop (pkg/cvo/cvo.go)
- The `Operator` struct is the central controller
- Watches the `ClusterVersion` resource in cluster
- Implements a reconciliation loop that is the single writer of `ClusterVersion` status
- Uses a work queue pattern with 15 max retries for failed operations
- Periodically checks for updates from configured upstream server
- Maintains conditions: `Progressing`, `Available`, `Failing`, `Upgradeable`, `RetrievedUpdates`

#### SyncWorker (pkg/cvo/sync_worker.go)
- Abstracts payload synchronization via `ConfigSyncWorker` interface
- Manages the actual application of manifests from release payload images
- Handles payload retrieval, verification, and application
- Uses rate limiting and retry logic for resilient updates
- Reports status back to main operator via `SyncWorkerStatus` channel

#### Payload Management (pkg/payload/)
- `State` enum defines update modes: `UpdatingPayload`, `ReconcilingPayload`, `InitializingPayload`, `PrecreatingPayload`
- `UpdatingPayload`: Conservative ordering when transitioning versions, errors block dependent operators
- `ReconcilingPayload`: Maintains current state, recreates resources without strict ordering
- `InitializingPayload`: First-time deployment, fast progress with tolerance for transient errors
- Release payloads stored in two directories:
  - `manifests/`: CVO's own manifests
  - `release-manifests/`: Manifests for other cluster operators
- Task graph orchestrates ordered application of manifests based on dependencies

#### Resource Libraries (lib/)
- `resourcebuilder/`: Builds Kubernetes resources from manifests
- `resourceapply/`: Applies resources with proper merge semantics for different API types
- `resourcemerge/`: Merges resource specifications (handles fields like `hostUsers` flag)
- `resourceread/`: Reads and validates manifests
- `capability/`: Manages cluster capability filtering

#### Update Service Integration (pkg/cincinnati/)
- Communicates with OpenShift Update Service (Cincinnati) to fetch available updates
- Parses update graphs with nodes (release versions) and edges (upgrade paths)
- Supports conditional edges based on cluster conditions

#### Preconditions (pkg/payload/precondition/)
- Validates cluster readiness before applying updates
- Checks prevent upgrades when cluster is in unhealthy state
- ClusterVersion-specific preconditions in `precondition/clusterversion/`

### Entry Points

#### cmd/cluster-version-operator/main.go
- Uses cobra for CLI structure
- Actual start logic delegated to `pkg/start/` package

#### pkg/start/start.go
- Initializes and launches core CVO control loops
- Sets up client connections, informers, and signal handling
- Creates and starts the main `Operator` instance

### Key Patterns

#### Release Image Structure
- Release images are OCI container images containing:
  - CVO binary at a specific version
  - Manifest files in `manifests/` and `release-manifests/`
  - `release-metadata` file with Cincinnati update graph info
  - `image-references` file mapping component names to images

#### Update Flow
1. CVO fetches available updates from configured upstream (OSUS)
2. Updates stored in `status.availableUpdates` of `ClusterVersion` resource
3. User/admin sets `spec.desiredUpdate` to trigger upgrade
4. CVO retrieves new release payload image
5. Validates payload signatures (if signature verification enabled)
6. Applies manifests systematically using task graph
7. Monitors cluster operator readiness during rollout
8. Updates `ClusterVersion` status conditions throughout process

#### Manifest Application Order
- Task graph in `pkg/payload/task_graph.go` determines ordering
- Dependencies between operators enforced during updates
- Parallel application during reconciliation for faster recovery

## Development Workflows

### Testing changes locally
```bash
# Build custom release payload with development CVO
oc adm release new \
  --from-release=quay.io/openshift-release-dev/ocp-release:${TAG} \
  --to-image-base=${REPO}/origin-cluster-version-operator:latest \
  --to-image=${REPO}/ocp-release:latest

# Inject into running cluster (disposable test clusters only!)
pullspec=${REPO}/ocp-release:latest
imagePatch='{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"'"$pullspec"'"}'
releaseImageArgPatch='{"op":"replace","path":"/spec/template/spec/containers/0/args/1","value":"--release-image='"$pullspec"'"}'
oc patch -n openshift-cluster-version deployment cluster-version-operator --type json --patch="[$imagePatch,$releaseImageArgPatch]"
```

### Debugging in cluster
```bash
# Check CVO deployment and pods
oc get deployment -n openshift-cluster-version cluster-version-operator
oc get pods -n openshift-cluster-version -l k8s-app=cluster-version-operator

# View logs
oc logs -n openshift-cluster-version -l k8s-app=cluster-version-operator

# Inspect ClusterVersion status
oc get clusterversion version -o yaml
oc adm upgrade
```

## Common File Locations

- **Manifests**: `install/` - YAML files for CVO deployment itself
- **Build scripts**: `hack/` - Shell scripts for building, testing, pushing
- **Integration tests**: `test/` - Separate integration test suite
- **Dev docs**: `docs/dev/` - Additional development guides
  - `feed-cvo-custom-graphs.md` - Testing with custom update graphs
  - `run-cvo-locally.md` - Running CVO outside cluster

## Commit Message Format

Follow the conventional format with subsystem prefix:
```text
<subsystem>: <what changed>

<why this change was made>

Fixes #<issue-number>
```

Subsystems include: `pkg/cvo`, `pkg/payload`, `lib/resourceapply`, `hack`, etc.

## Important Notes

- CVO runs as a single replica in the `openshift-cluster-version` namespace when deployed using the repository Kubernetes manifest files
- Never test against production clusters - use disposable test environments
- The operator uses leader election to ensure single active instance
- Release images must be verified before application (signature checking)
- Capabilities system filters manifests based on cluster capability set
- The `ClusterVersion` resource follows Kubernetes conventions: `spec` = desired, `status` = observed