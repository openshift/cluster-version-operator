# Code Review Subagent for Claude Code

## Overview

This directory contains a specialized code review subagent designed for the OpenShift Cluster Version Operator (CVO) repository. The agent leverages Claude Code's Task tool system to provide automated, structured code reviews.

## What the Agent Does

The code review agent:
- Analyzes Go code changes for correctness, security, and maintainability
- Applies CVO-specific domain knowledge (release payloads, task graphs, feature gates)
- Runs a comprehensive checklist covering 7 key areas
- Produces structured JSON output with actionable findings
- Assigns severity levels and provides specific line-number references
- Gives a clear verdict: LGTM, Minor, Major, or Reject

## Files in This Directory

- **code-review-agent.json** - Agent definition with prompt and configuration
- **review.sh** - Bash wrapper to invoke the agent easily
- **CODE_REVIEW_AGENT.md** - This file

## Quick Start

### Method 1: Using the Bash Wrapper (Recommended)

```bash
# Review your current branch against origin/main
./test/review.sh

# Review a specific pull request
./test/review.sh --pr 1273

# Review specific commits
./test/review.sh --range HEAD~3..HEAD

# Review specific files only
./test/review.sh --files pkg/cvo/cvo.go pkg/payload/payload.go

# Save output to custom location
./test/review.sh --output my-review.json
```

### Method 2: Direct Invocation in Claude Code

When working in Claude Code, you can directly invoke the code review agent:

```
Please review the changes in my current branch using the code-review agent.
Use the Task tool with the following:
- subagent_type: general-purpose
- Load the prompt from test/code-review-agent.json
- Provide the git diff and changed files as context
```

### Method 3: Manual Usage

1. Get your diff:
   ```bash
   git diff origin/main...HEAD > /tmp/my-changes.diff
   ```

2. Copy the prompt template from [code-review-agent.json](code-review-agent.json)

3. Provide to Claude Code with your diff

## Review Checklist

The agent evaluates changes across these dimensions:

### 1. **Correctness** (Critical)
- Logic errors, nil checks, race conditions
- Proper error handling and propagation
- Edge cases: empty inputs, boundaries, concurrent access
- Kubernetes resource lifecycle handling

### 2. **Testing** (High Priority)
- Unit test coverage for new logic
- Integration tests for end-to-end flows
- Table-driven test patterns
- Error path testing
- Proper use of fakes/mocks

### 3. **Go Idioms and Style** (Medium Priority)
- Naming conventions (exported vs unexported)
- Idiomatic error handling (errors.Wrap, fmt.Errorf)
- Context usage and cancellation
- Resource cleanup with defer
- Goroutine and channel best practices

### 4. **Security** (Critical)
- No secrets or credentials in code/logs
- Command injection prevention
- RBAC permissions correctness
- Input validation
- TLS certificate validation
- Image signature verification

### 5. **Performance** (Medium Priority)
- Allocation efficiency in hot paths
- Proper data structure selection (maps vs slices)
- API server query optimization (avoid N+1)
- Rate limiting and backoff
- Resource leak prevention

### 6. **Maintainability** (Medium Priority)
- Function length and complexity
- Clear variable naming
- Comments for complex logic
- Consistent error messages
- Appropriate logging levels (V(2), V(4))

### 7. **CVO-Specific** (High Priority)
- Release payload verification
- Task graph dependencies
- ClusterVersion status updates
- Upgrade compatibility
- Metrics naming/cardinality
- Feature gate handling
- Capability filtering

## Output Format

The agent produces JSON output:

```json
{
  "summary": "Brief description of the change",
  "verdict": "LGTM | Minor | Major | Reject",
  "top_risks": [
    "Risk 1: Nil pointer in edge case",
    "Risk 2: Missing integration test",
    "Risk 3: Potential race condition"
  ],
  "findings": [
    {
      "file": "pkg/cvo/cvo.go",
      "line_or_range": "142",
      "severity": "high",
      "category": "correctness",
      "message": "Potential nil pointer dereference when config.Spec is nil",
      "suggestion": "Add nil check: if config.Spec == nil { return nil, fmt.Errorf(...) }"
    }
  ],
  "positive_observations": [
    "Excellent use of table-driven tests",
    "Good error wrapping with context"
  ]
}
```

### Verdict Meanings

- **LGTM**: Ready to merge. No blocking issues.
- **Minor**: Small fixes needed (typos, comments). Quick iteration.
- **Major**: Significant changes required (logic errors, missing tests). Re-review needed.
- **Reject**: Fundamental issues (security, wrong approach, breaks compatibility).

## Integration with CI/CD

### GitHub Actions Example

```yaml
name: Code Review
on: [pull_request]

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
        with:
          fetch-depth: 0

      - name: Run Code Review Agent
        run: |
          ./test/review.sh --range origin/${{ github.base_ref }}...${{ github.sha }} \
            --output review-results.json

      - name: Post Review Comments
        uses: actions/github-script@v6
        with:
          script: |
            const fs = require('fs');
            const review = JSON.parse(fs.readFileSync('review-results.json'));

            // Post findings as PR comments
            for (const finding of review.findings) {
              if (finding.severity === 'high' || finding.severity === 'medium') {
                await github.rest.pulls.createReviewComment({
                  owner: context.repo.owner,
                  repo: context.repo.repo,
                  pull_number: context.issue.number,
                  body: `**${finding.severity.toUpperCase()}**: ${finding.message}`,
                  path: finding.file,
                  line: parseInt(finding.line_or_range)
                });
              }
            }
```

### GitLab CI Example

```yaml
code-review:
  stage: review
  script:
    - ./test/review.sh --range origin/main...HEAD --output review.json
    - cat review.json | jq -r '.findings[] | select(.severity=="high") | .message'
  artifacts:
    reports:
      codequality: review.json
```

## Customization

### Adjusting the Agent Prompt

Edit [code-review-agent.json](code-review-agent.json) to:
- Add repository-specific rules
- Adjust severity thresholds
- Include additional checklist items
- Change output format

### Adding Custom Checks

For project-specific validation:

```bash
# Create custom pre-check script
cat > test/custom-checks.sh <<'EOF'
#!/bin/bash
# Add custom validation before code review
./hack/verify-yaml
./hack/verify-generated-code
EOF

# Modify review.sh to call it
```

## Best Practices

1. **Run locally before pushing**: Catch issues early
   ```bash
   ./test/review.sh --files $(git diff --name-only HEAD)
   ```

2. **Review incrementally**: Don't wait for large PRs
   ```bash
   ./test/review.sh --range HEAD~1..HEAD  # Review each commit
   ```

3. **Combine with linters**: Use alongside golangci-lint, staticcheck
   ```bash
   golangci-lint run ./...
   ./test/review.sh
   ```

4. **Focus on high/medium severity**: Triage findings by severity

5. **Iterate**: Use Minor/Major verdicts to guide revision cycles

## Limitations

- **Context window**: Very large diffs (>100 files) may need splitting
- **Not a replacement for human review**: Use as a first-pass filter
- **Go-specific**: Optimized for Go code in CVO
- **Requires git**: Relies on git for diff generation

## Troubleshooting

### "No changes found to review"
```bash
# Check your diff range
git diff origin/main...HEAD

# Ensure you've fetched latest
git fetch origin
```

### "Claude Code CLI not found"
```bash
# Install Claude Code
npm install -g @anthropic-ai/claude-code

# Or use manual method
./test/review.sh  # Follow printed instructions
```

### Agent times out on large diffs
```bash
# Split review by directory
./test/review.sh --files pkg/cvo/*.go
./test/review.sh --files pkg/payload/*.go

# Or review commit-by-commit
for sha in $(git rev-list origin/main..HEAD); do
  ./test/review.sh --range ${sha}^..${sha}
done
```

## Contributing

To improve the code review agent:

1. Test changes:
   ```bash
   # Edit code-review-agent.json
   vim test/code-review-agent.json

   # Test with a known PR
   ./test/review.sh --pr 1273
   ```

2. Validate JSON output:
   ```bash
   jq empty review-output.json  # Should exit 0
   ```

3. Update documentation in this file

## Related Resources

- [CVO Architecture Documentation](../CLAUDE.md)
- [OpenShift Testing Guide](https://github.com/openshift/release/blob/master/ci-operator/README.md)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Kubernetes API Conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)

## Support

For issues or questions:
- File an issue in the CVO repository
- Reach out to the OpenShift CVO team
- Consult the [development docs](../docs/dev/)
