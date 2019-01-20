# CVO Metrics

The Cluster Version Operator reports the following metrics:

Core metrics for the version. Types:

* `current` - the version the operator is applying right now (the running CVO version)
* `failure` - if the failure condition is set, reported for desired and current versions
* `desired` - reported if different from current
* `completed` - returns 1 and the info of the most recent `Completed` entry in the history, or 0 otherwise

`current` has the `age` label set to the unix timestamp of the image age, `completed` has the `age` label
set to the time at which the update was completed. All other variants have empty age labels.

```
# HELP cluster_version Reports the version of the cluster.
# TYPE cluster_version gauge
cluster_version{age="132049356",image="test/image:1",type="current",version="4.0.2"} 1
cluster_version{age="",image="test/image:1",type="failure",version="4.0.2"} 1
cluster_version{age="",image="test/image:2",type="desired",version="4.0.3"} 1
cluster_version{age="132064396",image="test/image:1",type="completed",version="4.0.2"} 1
# HELP cluster_version_available_updates Report the count of available versions for an upstream and channel.
# TYPE cluster_version_available_updates gauge
cluster_version_available_updates{channel="fast",upstream="https://api.openshift.com/api/upgrades_info/v1/graph"} 0
```

Metrics about cluster operators:

```
# HELP cluster_operator_conditions Report the conditions for active cluster operators. 0 is False and 1 is True.
# TYPE cluster_operator_conditions gauge
cluster_operator_conditions{condition="Available",name="version",namespace="openshift-cluster-version"} 1
cluster_operator_conditions{condition="Failing",name="version",namespace="openshift-cluster-version"} 0
cluster_operator_conditions{condition="Progressing",name="version",namespace="openshift-cluster-version"} 0
cluster_operator_conditions{condition="RetrievedUpdates",name="version",namespace="openshift-cluster-version"} 0
# HELP cluster_operator_up Reports key highlights of the active cluster operators.
# TYPE cluster_operator_up gauge
cluster_operator_up{name="version",namespace="openshift-cluster-version",version="4.0.1"} 1
```

Metrics reported while applying the image:

```
# HELP cluster_version_payload Report the number of entries in the image.
# TYPE cluster_version_payload gauge
cluster_version_payload{type="applied",version="4.0.3"} 0
cluster_version_payload{type="pending",version="4.0.3"} 1
# HELP cluster_operator_payload_errors Report the number of errors encountered applying the image.
# TYPE cluster_operator_payload_errors gauge
cluster_operator_payload_errors{version="4.0.3"} 10
```