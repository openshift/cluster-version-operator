# How to Build and Run CVO Locally

This document provides the instructions to build and run the CVO executable locally. Local execution of CVO, for example executing CVO on your development laptop, allows a "quick-turn" test capability whereby the CVO executable can be quickly rebuilt with changes and launched again without the need of starting a new OCP cluster.

## Preconditions

Build the CVO executable using the standard procedure:

```console
$ cd ~/go/src/github.com/openshift/cluster-version-operator
$ make
```

Start an OCP cluster of the appropriate version. One that is compatiable with the version of CVO with which you're working.

Login to the cluster using `oc`.

Extract and download the OCP cluster release:

```console
$ oc image extract -a ~/pull-secret-internal quay.io/openshift-release-dev/ocp-release:4.4.0-rc.4-x86_64 --path /:/tmp/release/
```

## Steps to Run CVO Locally

Scale down the CVO pod on the cluster:

```console
$ oc scale --replicas=0 deployment.apps/cluster-version-operator -n openshift-cluster-version
```

Set the following environment variables used by CVO:

```console
# Set to anything
$ export NODE_NAME=foobar

# Set to release extracted above
$ export PAYLOAD_OVERRIDE=/tmp/release/
```

Run the CVO executable specifying `start`, the appropriate release image and, optionally, logging verbosity:

```console
$ ./_output/linux/amd64/cluster-version-operator -v5 start --release-image 4.4.0-rc.4
```

## Limitations

If the CVO is running locally using a binary it will not be able to handle upgrades since the upgrade process relies on starting another pod that mounts the same hostpath as the original CVO pod.
