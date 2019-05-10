# ClusterOperator Custom Resource

The ClusterOperator is a custom resource object which holds the current state of an operator. This object is used by operators to convey their state to the rest of the cluster.

Ref: [godoc](https://godoc.org/github.com/openshift/api/config/v1#ClusterOperator) for more info on the ClusterOperator type.

## Why I want ClusterOperator Custom Resource in /manifests

Everyone installed by the ClusterVersionOperator must include the ClusterOperator Custom Resource in [`/manifests`](operators.md#what-do-i-put-in-manifests).
The CVO sweeps the release image and applies it to the cluster. On upgrade, the CVO uses clusteroperators to confirm successful upgrades.
Cluster-admins make use of these resources to check the status of their clusters.

## How should I include ClusterOperator Custom Resource in /manifests

### How ClusterVersionOperator handles ClusterOperator in release image

When ClusterVersionOperator encounters a ClusterOperator Custom Resource,

- It uses the `.metadata.name` to find the corresponding ClusterOperator instance in the cluster
- It then waits for the instance in the cluster until
  - `.status.versions[name=operator].version` in the live instance matches the `.status.version` from the release image and
  - the live instance `.status.conditions` report available
- It then continues to the next task.

ClusterVersionOperator will only deploy files with `.yaml`, `.yml`, or `.json` extensions, like `kubectl create -f DIR`.

**NOTE**: ClusterVersionOperator sweeps the manifests in the release image in alphabetical order, therefore if the ClusterOperator Custom Resource exists before the deployment for the operator that is supposed to report the Custom Resource, ClusterVersionOperator will be stuck waiting and cannot proceed. Also note that the ClusterOperator resource in `/manifests` is only a communication mechanism, to tell the ClusterVersionOperator, which ClusterOperator resource to wait for. The ClusterVersionOperator does not create the ClusterOperator resource, creating and updating it is the responsibility of the respective operator.

### What should be the contents of ClusterOperator Custom Resource in /manifests

There are 2 important things that need to be set in the ClusterOperator Custom Resource in /manifests for CVO to correctly handle it.

- `.metadata.name`: name for finding the live instance
- `.status.versions[name=operator].version`: this is the version that the operator is expected to report. ClusterVersionOperator only respects the `.status.conditions` from instances that report their version.

Example:

For a cluster operator `my-cluster-operator`, that is reporting its status using a ClusterOperator instance named `my-cluster-operator`.

The ClusterOperator Custom Resource in /manifests should look like,

```yaml
apiVersion: config.openshift.io/v1
kind: ClusterOperator
metadata:
  name: my-cluster-operator
spec: {}
status:
  versions:
    - name: operator
      # The string "0.0.1-snapshot" is substituted in the manifests when the payload is built
      version: "0.0.1-snapshot" 
```

## What should an operator report with ClusterOperator Custom Resource

The ClusterOperator exists to communicate status about a functional area of the cluster back to both an admin
and the higher level automation in the CVO in an opinionated and consistent way. Because of this, we document
expectations around the outcome and have specific guarantees that apply.

Of note, in the docs below we use the word `operand` to describe the "thing the operator manages", which might be:

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

1. A operator doesn't report the `Available` status condition the first time
   until they are completely rolled out (or within some reasonable percentage if
   the component must be installed to all nodes)
2. An operator reports `Degraded` when its current state does not match its
   desired state over a period of time resulting in a lower quality of service.
   The period of time may vary by component, but a `Degraded` state represents
   persistent observation of a condition.  As a result, a component should not
   oscillate in and out of `Degraded` state. A service may be `Available` even
   if its degraded.  For example, your service may desire 3 running pods, but 1
   pod is crash-looping. The service is `Available` but `Degraded` because it
   may have a lower quality of service.  A component may be `Progressing` but
   not `Degraded` because the transition from one state to another does not
   persist over a long enough period to report `Degraded`.  A service should not
   report `Degraded` during the course of a normal upgrade. A service may report
   `Degraded` in response to a persistent infrastructure failure that requires
   administrator intervention. For example, if a control plane host is unhealthy
   and must be replaced.  An operator should report `Degraded` if unexpected
   errors occur over a period, but the expectation is that all unexpected errors
   are handled as operators mature.
3. An operator reports `Progressing` when it is rolling out new code,
   propagating config changes, or otherwise moving from one steady state to
   another. It should not report progressing when it is reconciling a previously
   known state. If it is progressing to a new version, it should include the
   version in the message for the condition like "Moving to v1.0.1".
4. An operator reports `Upgradeable` as `false` when it wishes to prevent an
   upgrade for an admin-correctable condition. The component should include a
   message that describes what must be fixed.
5. An operator reports a new version when it has rolled out the new version to
   all of its operands.

### Status

The operator should ensure that all the fields of `.status` in ClusterOperator are atomic changes. This means that all the fields in the `.status` are only valid together and do not partially represent the status of the operator.

### Version

The operator reports an array of versions. A version struct has a name, and a version. There MUST be a version with the name `operator`, which is watched by the CVO to know if a cluster operator has achieved the new level. The operator MAY report additional versions of its underlying operands.

Example:

```yaml
apiVersion: config.openshift.io/v1
kind: ClusterOperator
metadata:
  name: kube-apiserver
spec: {}
status:
  ...
  versions:
    - name: operator
      # Watched by the CVO
      version: 4.0.0-0.alpha-2019-03-05-054505
    - name: kube-apiserver
      # Used to report underlying upstream version
      version: 1.12.4
```

#### Version reporting during an upgrade

When your operator begins rolling out a new version it must continue to report the previous operator version in its ClusterOperator status. While any of your operands are still running software from the previous version then you are in a mixed version state, and you should continue to report the previous version. As soon as you can guarantee you are not and will not run any old versions of your operands, you can update the operator version on your ClusterOperator status.
