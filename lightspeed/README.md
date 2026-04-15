# OpenShift Lightspeed Integration

[Red Hat OpenShift Lightspeed][docs] is a generative AI service that helps developers and administrators solve problems by providing context-aware recommendations for OpenShift Container Platform.
This directory contains skills ([Claude Skills][claude-skills], [OpenAI Skills][openai-skills]) maintained by the cluster-version operator maintainers, which are designed to help agents with ClusterVersion activities such as preparing for cluster updates.

[`Containerfile.skills`](Containerfile.skills) builds a container image with [the skills](skill) in `/skill/`.
OpenShift policy does not currently allow `FROM scratch` images, so we're just using the stock `FROM` base image that we use in the cluster-version operator's [`Dockerfile.rhel`](../Dockerfile.rhel).
The resulting image can be [mounted as an image volume with a `subPath`][image-volume-sub-path] into any container that wishes to consume the skills.

[claude-skills]: https://code.claude.com/docs/en/skills
[docs]: https://docs.redhat.com/en/documentation/red_hat_openshift_lightspeed/1.0/html/about/ols-about-openshift-lightspeed
[openai-skills]: https://developers.openai.com/api/docs/guides/tools-skills
[image-volume-sub-path]: https://kubernetes.io/docs/tasks/configure-pod-container/image-volumes/#use-subpath-or-subpathexpr
