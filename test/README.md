# CVO Test Directory

This directory contains integration tests and development tools for the Cluster Version Operator (CVO).

## Contents

### Integration Tests
- **cvo/** - CVO-specific integration test suites
- **oc/** - OpenShift CLI test utilities and helpers

These tests are executed as part of OpenShift's test extension framework. See the [main CVO documentation](../CLAUDE.md#testing-commands) for how to run them.

### Code Review Agent 🤖

An automated code review subagent for Claude Code that provides structured analysis of code changes.

**Quick Start:**
```bash
# Review your current branch
./test/review.sh

# Review a specific PR
./test/review.sh --pr 1273
```

**Documentation:**
- [CODE_REVIEW_AGENT.md](CODE_REVIEW_AGENT.md) - Full documentation and setup guide
- [example-review-usage.md](example-review-usage.md) - Practical examples and recipes
- [code-review-agent.json](code-review-agent.json) - Agent configuration

**Key Features:**
- ✅ CVO-specific domain knowledge (task graphs, feature gates, payloads)
- ✅ Comprehensive checklist: correctness, security, performance, tests
- ✅ Structured JSON output with severity levels
- ✅ Integration with CI/CD pipelines
- ✅ Git hook support for pre-push/pre-commit validation

See [CODE_REVIEW_AGENT.md](CODE_REVIEW_AGENT.md) for complete usage instructions.

## Running Integration Tests

Integration tests require a disposable OpenShift cluster with admin credentials:

```bash
# Set KUBECONFIG to your test cluster
export KUBECONFIG=/path/to/test-cluster-kubeconfig

# Run all integration tests
make integration-test

# Or use gotestsum directly
gotestsum --packages="./test/..." -- -test.v -timeout 30m
```

**⚠️ Warning:** Never run integration tests against production clusters. These tests modify cluster state and can disrupt operations.

## Test Organization

Tests follow OpenShift's extension test framework:

```
test/
├── cvo/
│   └── cvo.go              # Main CVO integration test suite
├── oc/
│   ├── api/                # Kubernetes API helpers
│   ├── cli/                # OpenShift CLI utilities
│   └── oc.go               # oc command wrappers
└── ...
```

### Writing New Tests

When adding integration tests:

1. **Use Ginkgo/Gomega**: Follow OpenShift testing conventions
   ```go
   var _ = g.Describe("[Feature:ClusterVersionOperator] CVO", func() {
       g.It("should reconcile payloads", func() {
           // Test implementation
       })
   })
   ```

2. **Add test metadata**: Update `.openshift-tests-extension/openshift_payload_cluster-version-operator.json`

3. **Use helper utilities**: Leverage `test/oc` package for cluster operations
   ```go
   import "github.com/openshift/cluster-version-operator/test/oc"

   client := oc.NewClient()
   cv, err := client.GetClusterVersion(ctx, "version")
   ```

4. **Clean up resources**: Ensure tests clean up after themselves
   ```go
   defer func() {
       client.Delete(ctx, testNamespace)
   }()
   ```

5. **Mark destructive tests**: Use `[Serial]` for tests that can't run in parallel
   ```go
   var _ = g.Describe("[Serial] CVO upgrade", func() { ... })
   ```

## Test Metadata

Test metadata for OpenShift CI is in:
- `.openshift-tests-extension/openshift_payload_cluster-version-operator.json`

This file registers CVO tests with the OpenShift test suite, defining:
- Test names and descriptions
- Resource requirements
- Lifecycle (blocking vs. informing)
- Environment selectors

Update this file when adding new test scenarios.

## CI/CD Integration

### Prow Jobs

CVO tests run in OpenShift CI via Prow. Test results and artifacts are available at:
- https://prow.ci.openshift.org/

Search for "cluster-version-operator" to find relevant jobs.

### Code Review in CI

Integrate the code review agent into your CI pipeline:

```yaml
# Example: .github/workflows/review.yml
name: Code Review
on: [pull_request]
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Review PR
        run: ./test/review.sh --output review.json
      - uses: actions/upload-artifact@v3
        with:
          name: code-review
          path: review.json
```

## Development Workflow

1. **Make changes** to CVO code
2. **Run unit tests**: `make test`
3. **Review changes**: `./test/review.sh`
4. **Fix issues** identified in review
5. **Run integration tests** (on test cluster): `make integration-test`
6. **Commit** with conventional format: `pkg/cvo: fix synchronization bug`

## Related Documentation

- [CVO Architecture](../CLAUDE.md) - Main CVO documentation
- [Development Guide](../docs/dev/README.md) - Building and testing CVO
- [OpenShift Testing](https://github.com/openshift/release/blob/master/ci-operator/README.md)

## Getting Help

- File issues in the CVO repository
- Check Prow job logs for test failures
- Review code with the automated agent first to catch common issues
- Reach out to the OpenShift CVO team on Slack

---

**💡 Tip:** Start every PR by running `./test/review.sh` to catch issues early!
