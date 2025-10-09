Cluster Bot testing...
# Cluster Version Operator

The Cluster Version Operator (CVO) is one of the [Cluster Operators][dev-guide-operators] that run in every OpenShift
cluster. CVO consumes an artifact called a _"release payload image,"_ which represents a specific version of OpenShift.
The release payload image contains the [resource manifests][kube-glossary-manifest] necessary for the cluster to
function, like all Cluster Operator ones. CVO reconciles the resources within the cluster to match the manifests in the
release payload image. As a result, CVO implements _cluster upgrades_. After being provided a release payload image for
a newer OpenShift version, CVO reconciles all Cluster Operators to their updated versions, and Cluster Operators
similarly update their operands.

## OpenShift Upgrades

For information about upgrading OpenShift clusters, please see the respective documentation:

- [OKD Documentation: Updating clusters][okd-updating-clusters]
- [Red Hat OpenShift Container Platform: Updating clusters][ocp-updating-clusters]

## `ClusterVersion` Resource

Like other Cluster Operators, the Cluster Version Operator is configured by a Config API resource in the cluster:
a [`ClusterVersion`][ocp-clusterversion]:

```console
$ oc explain clusterversion
  KIND:     ClusterVersion
  VERSION:  config.openshift.io/v1

  DESCRIPTION:
       ClusterVersion is the configuration for the ClusterVersionOperator. This is
       where parameters related to automatic updates can be set. Compatibility
       level 1: Stable within a major release for a minimum of 12 months or 3
       minor releases (whichever is longer).

  FIELDS:
     ...
     spec	<Object> -required-
       spec is the desired state of the cluster version - the operator will work
       to ensure that the desired version is applied to the cluster.

     status	<Object>
       status contains information about the available updates and any in-progress
       updates.
```

`ClusterVersion` resource follows the established [Kubernetes pattern][kube-spec-and-status] where the `spec`
property describes the desired state that CVO should achieve and maintain, and the `status` property is populated by the
CVO to describe its status and the observed state.

In a typical OpenShift cluster, there will be a cluster-scoped `ClusterVersion` resource called `version`:

```console
$ oc get clusterversion version
NAME      VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.11.17   True        False         6d5h    Cluster version is 4.11.17
```

Note that as a user or a cluster administrator, you usually do not interact with the `ClusterVersion` resource directly
but via either the [`oc adm upgrade`][ocp-oc-adm-upgrade] CLI or the [web console][ocp-webconsole-upgrades].

## Understanding Upgrades

> :bulb: This section is only a high-level overview. See the [Update Process][dev-guide-upgrade-workflow] and
> [Reconciliation][dev-guide-reconciliation] documents in the [dev-guide][dev-guide] for more details.

The Cluster Version Operator continuously fetches information about upgrade paths for the configured channel from the
OpenShift Update Service (OSUS). It stores the recommended update options in the `status.availableUpdates` field of
its `ClusterVersion` resource.

The intent to upgrade the cluster to another version is expressed by storing the desired version in
the `spec.desiredUpdate` field. When `spec.desiredUpdate` does not match the current cluster version, CVO will start
updating the cluster. It downloads the release payload image, validates it,
and [systematically reconciles][dev-guide-reconciliation] the Cluster Operator resources to match the updated manifests
delivered in the release payload image.

## Troubleshooting

A typical OpenShift cluster will have a `Deployment` resource called `cluster-version-operator` in
the `openshift-cluster-version` namespace, configured to run a single CVO replica. Confirm that its `Pod` is up and
optionally inspect its log:

```console
$ oc get deployment -n openshift-cluster-version cluster-version-operator
NAME                       READY   UP-TO-DATE   AVAILABLE   AGE
cluster-version-operator   1/1     1            1           2y227d

$ oc get pods -n openshift-cluster-version -l k8s-app=cluster-version-operator
NAME                                       READY   STATUS    RESTARTS   AGE
cluster-version-operator-6885cc574-674n6   1/1     Running   0          6d5h

$ oc logs -n openshift-cluster-version -l k8s-app=cluster-version-operator
...
```

The CVO follows the [Kubernetes API conventions][kube-api-conventions-status] and sets `Conditions` in the status of
its `ClusterVersion` resource. These conditions are surfaced by both the OpenShift web console and the `oc adm upgrade`
CLI.

## Development

Contributions welcome! Please follow [CONTRIBUTING.md](CONTRIBUTING.md) and [developer documentation](./docs/dev).

[dev-guide]: https://github.com/openshift/enhancements/blob/master/dev-guide

[dev-guide-operators]: https://github.com/openshift/enhancements/blob/master/dev-guide/operators.md#what-is-an-openshift-clusteroperator

[dev-guide-reconciliation]: https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/user/reconciliation.md

[dev-guide-upgrade-workflow]: https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/user/update-workflow.md

[kube-api-conventions-status]: https://github.com/kubernetes/community/blob/4c9ef2d135294355e7ca33ec7a5e01d31438df12/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

[kube-spec-and-status]: https://kubernetes.io/docs/concepts/overview/working-with-objects/kubernetes-objects/#object-spec-and-status

[kube-glossary-manifest]: https://kubernetes.io/docs/reference/glossary/?all=true#term-manifest

[okd-updating-clusters]: https://docs.okd.io/latest/updating/index.html

[ocp-updating-clusters]: https://docs.openshift.com/container-platform/latest/updating/index.html

[ocp-clusterversion]: https://docs.openshift.com/container-platform/latest/rest_api/config_apis/clusterversion-config-openshift-io-v1.html

[ocp-oc-adm-upgrade]: https://docs.openshift.com/container-platform/latest/cli_reference/openshift_cli/administrator-cli-commands.html#oc-adm-upgrade

[ocp-webconsole-upgrades]: https://docs.openshift.com/container-platform/latest/updating/updating-cluster-within-minor.html#update-upgrading-web_updating-cluster-within-minor
