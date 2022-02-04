# release-resource-diff

# Description
release-resource-diff checks if OpenShift Container Platform (OCP) resources, as defined by the manifests for one or more OCP releases, exist in a given newly installed OCP release. The purpose is to produce a list of resources which could exist on an upgraded cluster but can and should be removed.

release-resource-diff has the following additional logic to handle the described special cases:

- **Deprecated APIs** - Per [Deprecated APIs Removed In 1.16: Here’s What You Need To Know](https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16), check if a deprecated resource exists as a migrated resource in the target release.
- **Manifest Delete Annotation** - If a resource manifest has been found containing the delete annotation it is already setup for removal and is therefore removed from the list of resources that should be removed.

# Usage

cluster-version-util release-resource-diff [flags]

Flags:
```
-h, --help              help for release-resource-diff
-o, --output string     results file path (optional)
-r, --releases string   top-level directory containing subdirectories for each OCP release to be checked
-t, --target string     file containing all resources from a running, newly installed, OCP release
-v, --verbose           verbose logging
```
The `--target` file contains all the resources from a running, newly installed, OCP release. This file is created by connecting to an OCP cluster running the OCP release to be compared against. The cluster must have been newly installed with the release, not upgraded, in order to get only the set of resources created by that specific release. Once connected to the cluster the file can be created by running `tools/create-target-release-file.sh TARGET_FILE_NAME` where `TARGET_FILE_NAME` is the path to the file to be created. This file will have 4 columns for each resource: APIVersion, Kind, Name, Namespace. The release-resource-diff utility depends on this file having these 4 columns in the given order. The file will be used for comparison against resources from other OCP releases to verify whether the resource still exists. For an example see [here](test/4.10.0-0.nightly-2022-01-27-104747-all-resources.txt).
  
The `--releases` top-level directory contains subdirectories for each OCP release to be checked. The subdirectories should be named using the release version number of its contents. For example, if the top-level directory were /tmp/releases it may contain subdirectories:
  
- 4.1.41
- 4.2.36
- ...
- 4.8.0-rc.0
  
These subdirectory names are used to fill in the "Born In" column of the results file. The results file also has a "Last In" column which indicates the latest release, of the given set being checked, in which the resource does exist. For example, if 4.2.36 resource R was not found in the target release but does exist in 4.8.0-rc.0 "Born In" would be 4.2.36 and "Last In" would be 4.8.0-rc.0. If the release numbering scheme is not used the program will not be able to determine "Last In" and it will simply be empty.

Each subdirectory is then populated with the manifests from that release by running `oc adm release extract`.

The utility produces a file containing the results. By default the file is `<releases top-level dir>/delete-candidates.txt`. Use the `-o` option to override the default. The file is not display friendly so it is recommended that it be opened in a spreadsheet program such as LibreOffice Calc for ease of viewing and to allow sorting.
