# Developing Cluster Version Operator

> :warning: This document only covers the basic development, building and testing tasks. For information about
> concepts used and implemented in CVO, please see the
> [dev-guide in the openshift/enhancements repository](https://github.com/openshift/enhancements/tree/master/dev-guide/cluster-version-operator).

## Building CVO

### Building the CVO binary

```console
$ make build
hack/build-go.sh
Using version from git...
Building github.com/openshift/cluster-version-operator (v1.0.0-942-g59e0826f-dirty)
```

### Building the CVO image

The [`hack/build-image.sh`](../../hack/build-image.sh) script builds a local CVO image:

```console
$ ./hack/build-image.sh
INFO: Using version from git...
...
[2/2] COMMIT cluster-version-operator:v1.0.0-942-g59e0826f-dirty
--> 98ff149964f
Successfully tagged localhost/cluster-version-operator:v1.0.0-942-g59e0826f-dirty
```

### Publishing a development CVO image

The [`hack/push-image.sh`](../../hack/push-image.sh) script publishes
a [locally-built CVO image](#building-the-cvo-image) to a remote repository:

```console
$ REPO=<your personal repo, such as quay.io/login> ./hack/push-image.sh
```

This pushes `${VERSION}` and `latest` tags to `${REPO}/origin-cluster-version-operator`, where `${VERSION}` encodes the
Git commit used to build the image.

### Building and publishing a release payload image with development CVO

After publishing a [development CVO image](#publishing-a-development-cvo-image), you can build a release payload image
that will contain all manifests from an existing release payload and the development CVO binary:

```console
$ oc adm release new --from-release=quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64 \
                     --to-image-base=${REPO}/origin-cluster-version-operator:latest \
                     --to-image=${REPO}/ocp-release:latest
```

Note that the pull secret for the `quay.io/openshift-release-dev/ocp-release` container image can be acquired [here](https://console.redhat.com/openshift/install/pull-secret).

In case you want to utilize different credentials for the same container image registry domain, for example, 
one set of credentials for the existing release image and one for a personal location where to upload the new release 
image in the same container image registry domain, the following unsupported methods are possible at the moment.
Note that these methods may not be supported or functional when used with other container tools.

The main idea is to modify the used [authentication file](https://docs.podman.io/en/latest/markdown/podman-login.1.html#authfile-path)
and specify a second `auth` entry that has a specified port or path, which acts as a new entry for which different 
credentials can be specified.

Given an authentication file with a specified path:

```json
{
  "auths": {
    "quay.io/username": {
      "auth": "token to be used when accessing the username's namespace"
    },
    "quay.io": {
      "auth": "broad token to be used when the quay.io host is specified"
    }
  }
}
```

Build and publish a release image:

```console
$ oc adm release new --from-release=quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64 \
                     --to-image-base=quay.io/username/origin-cluster-version-operator:latest \
                     --to-image=quay.io/username/ocp-release:latest
```

Given a specified port in the authentication file:

```json
{
  "auths": {
    "quay.io:443": {
      "auth": "broad token to be used when the quay.io host and the 443 port are specified"
    },
    "quay.io": {
      "auth": "broad token to be used when the quay.io host is specified"
    }
  }
}
```

Build and publish a release image by specifying the port accordingly, for example:

```console
$ oc adm release new --from-release=quay.io/openshift-release-dev/ocp-release:4.11.18-x86_64 \
                     --to-image-base=quay.io:443/${NAMESPACE}/origin-cluster-version-operator:latest \
                     --to-image=quay.io:443/${NAMESPACE}/ocp-release:latest
```

## Testing CVO

### Unit tests

Run `make test` to execute all unit tests.

### Integration tests

Run `make integration-test` to execute integration tests against an OpenShift cluster. It requires `$KUBECONFIG` with
administrator credentials. Only execute integration tests against disposable testing clusters.

```console
$ export KUBECONFIG=<admin kubeconfig path>
$ make integration-test
```

### Replace cluster's CVO with development CVO release payload image

After publishing
a [release payload image with development CVO](#building-and-publishing-a-release-payload-image-with-development-cvo),
you can inject its pullspec into the `cluster-version-operator` Deployment in an existing cluster. Only do this against
disposable testing clusters.

```console
$ pullspec=${REPO}/ocp-release:latest

$ imagePatch='{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"'"$pullspec"'"}'
$ releaseImageArgPatch='{"op":"replace","path":"/spec/template/spec/containers/0/args/1","value":"--release-image='"$pullspec"'"}'
$ oc patch -n openshift-cluster-version deployment cluster-version-operator --type json --patch="[$imagePatch,$releaseImageArgPatch]"
deployment.apps/cluster-version-operator patched
```

### Advanced testing scenarios

- [Feed custom upgrade graphs to CVO](feed-cvo-custom-graphs.md)
- [Run local CVO against a remote cluster](run-cvo-locally.md)
