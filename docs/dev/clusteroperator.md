# ClusterOperator Custom Resource

The ClusterOperator is a custom resource object which holds the current state of an operator. This object is used by operators to convey their state to the rest of the cluster.

Ref: [godoc](https://godoc.org/github.com/openshift/api/config/v1#ClusterOperator) for more info on the ClusterOperator type.

## Why I want ClusterOperator Custom Resource in /manifests

Everyone must include the ClusterOperator Custom Resource in [`/manifests`](operators.md#what-do-i-put-in-manifests).
The ClusterVersionOperator sweeps the release image and applies it to the cluster. On upgrade, the CVO uses clusteroperators to confirm successful upgrades.
Cluster-admins make use of these resources to check the status of their clusters.

## How should I include ClusterOperator Custom Resource in /manifests

### How ClusterVersionOperator handles ClusterOperator in release image

When ClusterVersionOperator encounters a ClusterOperator Custom Resource,

- It uses the `.metadata.name` and `.metadata.namespace` to find the corresponding ClusterOperator instance in the cluster
- It then waits for the instance in the cluster until
  - `.status.version` in the live instance matches the `.status.version` from the release image and
  - the live instance `.status.conditions` report available, not progressing and not failed
- It then continues to the next task.

ClusterVersionOperator will only deploy files with `.yaml`, `.yml`, or `.json` extensions, like `kubectl create -f DIR`.

**NOTE**: ClusterVersionOperator sweeps the manifests in the release image in alphabetical order, therefore if the ClusterOperator Custom Resource exists before the deployment for the operator that is supposed to report the Custom Resource, ClusterVersionOperator will be stuck waiting and cannot proceed. Also note that the ClusterOperator resource in `/manifests` is only a communication mechanism, to tell the ClusterVersionOperator, which ClusterOperator resource to wait for. The ClusterVersionOperator does not create the ClusterOperator resource, this and updating it is the responsibility of the respective operator.

### What should be the contents of ClusterOperator Custom Resource in /manifests

There are 3 important things that need to be set in the ClusterOperator Custom Resource in /manifests for CVO to correctly handle it.

- `.metadata.namespace`: namespace for finding the live instance in cluster
- `.metadata.name`: name for finding the live instance in the namespace
- `.status.version`: this is the version that the operator is expected to report. ClusterVersionOperator only respects the `.status.conditions` from instance that reports `.status.version`

Example:

For a cluster operator `my-cluster-operator` applying version `1.0.0`, that is reporting its status using ClusterOperator instance `my-cluster-operator` in namespace `my-cluster-operator-namespace`.

The ClusterOperator Custom Resource in /manifests should look like,

```yaml
apiVersion: operatorstatus.openshift.io
kind: ClusterOperator
metadata:
  namespace: my-cluster-operator-namespace
  name: my-cluster-operator
status:
  version: 1.0.0
```

## What should an operator report with ClusterOperator Custom Resource

The ClusterOperator exists to communicate status about a functional area of the cluster back to both an admin
and the higher level automation in the CVO in an opinionated and consistent way. Because of this, we document
expectations around the outcome and have specific guarantees that apply.

Of note, in the docs below we use the word `operand` to descirbe the "thing the operator manages", which might be:

* A deployment or daemonset, like a cluster networking provider
* An API exposed via a CRD and the operator updates other API objects, like a secret generator
* Just some controller loop invariant that the operator manages, like "all certificate signing requests coming from valid machines are approved"

An operand doesn't have to be code running on cluster - that might be the operator. When we say "is the operand available" that might mean "the new code is rolled out" or "all old API objects have been updated" or "we're able to sign certificate requests"

Here are the guarantees components can get when they follow the rules we define:

1. Cause an installation to fail because a component is not able to become available for use
2. Cause an upgrade to hang because a component is not able to successfully reach the new upgrade
3. Prevent a user from clicking the upgrade button because components have one or more preflight criteria that are not met (e.g. nodes are at version 4.0 so the control plane can't be upgraded to 4.2 and break N-1 compat)
4. Ensure other components are upgraded *after* your component (guarantee "happens before" in upgrades, such as kube-apiserver being updated before kube-controller-manager)

There are a set of guarantees components are expected to honor in return:

1. A component doesn't report the `Available` status condition the first time until they are completely rolled out (or within some reasonable percentage if the component must be installed to all nodes)
2. A component reports `Failing` when it can't accomplish its task. This might be very broad - the API servers are down so I failed to update - or narrow - I couldn't update the last secret. In either case, Failing communicates something is wrong.
3. A component reports `Progressing` when it is rolling out new code, propagating config changes, or otherwise moving from one steady state to another. It should not report progressing when it is reconciling a previously known state.
4. A component reports `Upgradeable` as `false` when it wishes to prevent an upgrade for an admin-correctable condition. The component should include a message that describes what must be fixed.
5. A component reports when it has rolled out the 

### Status

The operator should ensure that all the fields of `.status` in ClusterOperator are atomic changes. This means that all the fields in the `.status` are only valid together and do not partially represent the status of the operator.

### Version

The operator should report a version which indicates the components that it is applying to the cluster.

### Conditions

Refer [the godocs](https://godoc.org/github.com/openshift/api/config/v1#ClusterStatusConditionType) for conditions.

In general, ClusterOperators should contain at least three core conditions:

* `Progressing` must be true if the operator is actually making change to the operand.
The change may be anything: desired user state, desired user configuration, observed configuration, version update, etc.
If this is false, it means the operator is not trying to apply any new state.
If it remains true for an extended period of time, it suggests something is wrong in the cluster.  It can probably wait until Monday.
* `Available` must be true if the operand is functional and available in the cluster at the level in status.
If this is false, it means there is an outage.  Someone is probably getting paged.
* `Failing` should be true if the operator has encountered an error that is preventing it or it's operand from working properly.
The operand may still be available, but intent may not have been fulfilled.
If this is true, it means that the operand is at risk of an outage or improper configuration.  It can probably wait until the morning, but someone needs to look at it.

The message reported for each of these conditions is important.  All messages should start with a capital letter (like a sentence) and be written for an end user / admin to debug the problem.  `Failing` should describe in detail (a few sentences at most) why the current controller is blocked. The detail should be sufficient for an engineer or support person to triage the problem. `Available` should convey useful information about what is available, and be a single sentence without punctuation.  `Progressing` is the most important message because it is shown by default in the CLI as a column and should be a terse, human-readable message describing the current state of the object in 5-10 words (the more succinct the better).

For instance, if the CVO is working towards 4.0.1 and has already successfully deployed 4.0.0, the conditions might be reporting:

* `Failing` is false with no message
* `Available` is true with message `Cluster has deployed 4.0.0`
* `Progressing` is true with message `Working towards 4.0.1`

If the controller reaches 4.0.1, the conditions might be:

* `Failing` is false with no message
* `Available` is true with message `Cluster has deployed 4.0.1`
* `Progressing` is false with message `Cluster version is 4.0.1`

If an error blocks reaching 4.0.1, the conditions might be:

* `Failing` is true with a detailed message `Unable to apply 4.0.1: could not update 0000_70_network_deployment.yaml because the resource type NetworkConfig has not been installed on the server.`
* `Available` is true with message `Cluster has deployed 4.0.0`
* `Progressing` is true with message `Unable to apply 4.0.1: a required object is missing`

The progressing message is the first message a human will see when debugging an issue, so it should be terse, succinct, and summarize the problem well.  The failing message can be more verbose. Start with simple, easy to understand messages and grow them over time to capture more detail.
