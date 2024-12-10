# How to Contribute

The cluster-version operator is [Apache 2.0 licensed](LICENSE) and accepts contributions via GitHub pull requests.
This document outlines some of the conventions on development workflow, commit message formatting, contact points and other resources to make it easier to get your contribution accepted.

## Security Response

If you've found a security issue that you'd like to disclose confidentially, please contact Red Hat's Product Security team.
Details [here][security].

## Getting Started

- Fork the repository on GitHub.
- Read the [README](README.md) and [developer documentation](docs/dev) for build and test instructions.
- Play with the project, submit bugs, submit patches!

### Contribution Flow

Anyone may [file issues][new-issue].
For contributors who want to work up pull requests, the workflow is roughly:

1. Create a topic branch from where you want to base your work (usually `main`).
2. Make commits of logical units.
3. Make sure your commit messages are in the proper format (see [below](#commit-message-format)).
4. Make sure your [code builds][build-cvo] and follows the [coding style](#coding-style)
5. Make sure the [tests pass][test-cvo] and add any new tests as appropriate.
6. Push your changes to a topic branch in your fork of the repository.
7. Submit a pull request to the original repository.
8. The [repo owners](OWNERS) will respond to your issue promptly, following [the usual Prow workflow][prow-review].

Thanks for your contributions!

## Coding Style

The Cluster Version Operator follows the common Golang [coding style][golang-style]. Please follow them when working
on your contributions. Use `go fmt ./...` to format your code.

## Commit Message Format

We follow a rough convention for commit messages that is designed to answer two questions: what changed and why.
The subject line should feature the what and the body of the commit should describe the why.

```
scripts: add the test-cluster command

this uses tmux to set up a test cluster that you can easily kill and
start for debugging.

Fixes #38
```

The format can be described more formally as follows:

```
<subsystem>: <what changed>
<BLANK LINE>
<why this change was made>
<BLANK LINE>
<footer>
```

The first line is the subject and should be no longer than 70 characters, the second line is always blank, and other lines should be wrapped at 80 characters.
This allows the message to be easier to read on GitHub as well as in various Git tools.

[golang-style]: https://github.com/golang/go/wiki/CodeReviewComments
[new-issue]: https://github.com/openshift/cluster-version-operator/issues/new
[prow-review]: https://github.com/kubernetes/community/blob/master/contributors/guide/owners.md#the-code-review-process
[security]: https://access.redhat.com/security/team/contact
[build-cvo]: ./docs/dev/README.md#building-cvo
[test-cvo]: ./docs/dev/README.md#testing-cvo
