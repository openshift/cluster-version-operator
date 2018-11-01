# CVO Metrics

The Cluster Version Operator reports the following metrics:

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
# HELP cluster_version Reports the version of the cluster.
# TYPE cluster_version gauge
cluster_version{payload="test/image:1",type="current",version="4.0.2"} 1
# HELP cluster_version_available_updates Report the count of available versions for an upstream and channel.
# TYPE cluster_version_available_updates gauge
cluster_version_available_updates{channel="fast",upstream="http://localhost:8080/graph"} 0
```