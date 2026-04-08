# Troubleshoot CVO on the bootstrap node

In the OpenShift installation process, a CVO pod runs on the [bootstrap node](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/installation_overview/ocp-installation-overview#installation-process-details). The pod's manifest is defined in [bootstrap-pod.yaml](../../bootstrap/bootstrap-pod.yaml) which is slightly differently from the one created for the [CVO deployment](../../install/0000_00_cluster-version-operator_30_deployment.yaml) running on a control plane after installation.

To troubleshoot issues from the CVO pod on the bootstrap node, it is useful digging into the installer log and log-bundle. See [OpenShift documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/validation_and_troubleshooting/installing-troubleshooting) for details about how they are collected.
In CI jobs, they are usually collected by a step during installation, such as [ipi-install-install](https://steps.ci.openshift.org/reference/ipi-install-install). See `.openshift_install-1775655648.log` and `log-bundle-20260408134042.tar` in the following files: 

```console
$ ls -A
.openshift_install-1775655648.log install-config.yaml
clusterapi_output-1775655648      log-bundle-20260408134042.tar
```

In "log-bundle", there are logs from the CVO pod and other manifests for inspection.
