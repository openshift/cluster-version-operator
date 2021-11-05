You might come across some scenarios when you want to give cluster verison operator some custom update graph which is not possible through the public instance of OpenShift update service.

Here are the steps

### Step-1

Create a JSON file with the custom graph.

Example:
```json
{
  "edges": [
    [
      0,
      1
    ]
  ],
  "nodes": [
    {
      "payload": "registry.build01.ci.openshift.org/ci-ln-cbv124k/release@sha256:8dc24e569e3aac801e8c9b47fef53bb4e80baaa469e342a257ca3cfa1a5ca0fb",
      "version": "4.10.0-0.ci.test-2021-11-04-124324-ci-ln-cbv124k-latest"
    },
    {
      "payload": "quay.io/openshift-release-dev/ocp-release@sha256:e867135cd5a09192635b46ccab6ca7543e642378dc72fa22ea54961b05e322f2",
      "version": "4.10.0-testing"
    }
  ]
}
```

### Step-2

Host the file in any location where the cluster version operator can access it.

There are many ways to host a static JSON. However adding steps around how it can be done in a git or github repository.
For example I have a file with custom graph in in https://raw.githubusercontent.com/LalatenduMohanty/cluster-version-operator/cinci-graph-test/graph.json

```sh
git checkout --orphan customized-cincinnati-graph
git rm -rf .   # we don't want any of the previous branch's files in this branch
cat <<EOF >cincinnati-graph.json
> {
>   "nodes": [...],
>   "edges": [...],
>   "conditionalEdges": [...]
> }
> EOF
jq . cincinnati-graph.json  # sanity check your syntax as valid JSON to catch missing commas and such
git add cincinnati-graph.json
git commit -m 'WIP: Static Cincinnati graph to test customized-cincinnati-graph'
git push -u "${YOUR_REMOTE_NAME}" customized-cincinnati-graph
```

### Step-3

Once you have the file hosted, set the spec.Upstream to point cluster version operator to this URL.

```sh
oc patch clusterversion/version --patch '{"spec":{"upstream":"<URL>"}}' --type=merge
```
Example:
```sh
oc patch clusterversion/version --patch '{"spec":{"upstream":"https://raw.githubusercontent.com/LalatenduMohanty/cluster-version-operator/cinci-graph-test/graph.json"}}' --type=merge
```

### Step-4

Set the channel for the cluster. It does not matter what channel you set as CVO will always get the same content of the JSON file from the upstream URL.

Example:
```sh
oc adm upgrade channel fast-4.10
```

Now you should be able to see the available updates

Example:
```console
$ oc adm upgrade
Cluster version is 4.10.0-0.ci.test-2021-11-04-124324-ci-ln-cbv124k-latest

Upstream: https://raw.githubusercontent.com/LalatenduMohanty/cluster-version-operator/cinci-graph-test/graph.json
Channel: fast-4.10
Updates:

VERSION        IMAGE
4.10.0-testing quay.io/openshift-release-dev/ocp-release@sha256:e867135cd5a09192635b46ccab6ca7543e642378dc72fa22ea54961b05e322f2
```
