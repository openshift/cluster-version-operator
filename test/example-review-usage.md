# Example: Using the Code Review Subagent

This document shows concrete examples of how to use the code review subagent in different scenarios.

## Example 1: Review Current Branch in Claude Code

When you're in a Claude Code session, simply ask:

```
Please review my current changes against origin/main using the code review agent.
```

Claude Code will:
1. Load the agent configuration from `test/code-review-agent.json`
2. Get the diff using `git diff origin/main...HEAD`
3. Invoke the Task tool with subagent_type="general-purpose"
4. Return structured findings in JSON format

## Example 2: Review a Specific PR

### Using the Shell Script

```bash
cd /path/to/cluster-version-operator
./test/review.sh --pr 1273
```

**Output:**
```
Fetching PR #1273...
Files changed:
pkg/cvo/cvo.go
pkg/payload/payload.go
...

Generating code review...

=== CODE REVIEW RESULTS ===

=== SUMMARY ===
Add feature gate based manifest inclusion with integration tests

Verdict: LGTM

Top Risks:
  - Risk 1: Feature gate filtering may exclude critical operators if misconfigured
  - Risk 2: Integration test coverage for edge cases (empty feature set) could be improved
  - Risk 3: Backward compatibility needs verification for clusters without feature gates

Review saved to: test/review-output.json
```

## Example 3: Review Only Modified Go Files

```bash
# Get list of modified .go files
MODIFIED_GO_FILES=$(git diff --name-only origin/main...HEAD | grep '\.go$')

# Review only those files
./test/review.sh --files $MODIFIED_GO_FILES
```

## Example 4: Incremental Review During Development

While developing a feature, review each commit:

```bash
# Review your last commit
./test/review.sh --range HEAD~1..HEAD

# Review last 3 commits individually
for i in {1..3}; do
  echo "=== Reviewing commit HEAD~$((i-1)) ==="
  ./test/review.sh --range HEAD~${i}..HEAD~$((i-1))
done
```

## Example 5: Pre-Push Hook Integration

Create a git pre-push hook to automatically review changes:

```bash
cat > .git/hooks/pre-push <<'EOF'
#!/bin/bash
# Pre-push hook to run code review

REMOTE="$1"
URL="$2"

# Only run for main branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [[ "$CURRENT_BRANCH" != "main" ]]; then
  echo "Running code review for branch: $CURRENT_BRANCH"
  ./test/review.sh --range origin/main...HEAD --output /tmp/pre-push-review.json

  # Check verdict
  VERDICT=$(jq -r '.verdict' /tmp/pre-push-review.json 2>/dev/null || echo "Unknown")

  if [[ "$VERDICT" == "Reject" ]]; then
    echo "❌ Code review FAILED: Reject verdict"
    echo "Please address critical issues before pushing."
    echo "Review details: /tmp/pre-push-review.json"
    exit 1
  elif [[ "$VERDICT" == "Major" ]]; then
    echo "⚠️  Code review found MAJOR issues"
    echo "Consider addressing before pushing."
    jq -r '.top_risks[]' /tmp/pre-push-review.json | sed 's/^/  - /'
  fi
fi

exit 0
EOF

chmod +x .git/hooks/pre-push
```

## Example 6: CI/CD Integration

### Prow Job Configuration

```yaml
# In ci-operator config
tests:
- as: code-review
  commands: |
    #!/bin/bash
    set -euo pipefail

    # Review changes in this PR
    ./test/review.sh --range origin/main...HEAD --output ${ARTIFACT_DIR}/review.json

    # Fail job if critical issues found
    HIGH_SEVERITY=$(jq '[.findings[] | select(.severity=="high")] | length' ${ARTIFACT_DIR}/review.json)
    if [[ $HIGH_SEVERITY -gt 0 ]]; then
      echo "Found $HIGH_SEVERITY high-severity issues"
      jq -r '.findings[] | select(.severity=="high") | "\(.file):\(.line_or_range) - \(.message)"' ${ARTIFACT_DIR}/review.json
      exit 1
    fi
  container:
    from: src
```

## Example 7: Filtering Review Results

Extract only high-severity security findings:

```bash
./test/review.sh --output review.json

jq -r '.findings[] |
  select(.severity=="high" and .category=="security") |
  "\(.file):\(.line_or_range)\n  \(.message)\n  Fix: \(.suggestion // "See details above")\n"' \
  review.json
```

**Output:**
```
pkg/cvo/cvo.go:245
  API token logged in debug statement - potential credential leak
  Fix: Use klog.V(4).Infof("token: [REDACTED]") instead

pkg/payload/payload.go:112
  Image signature verification skipped when URL starts with "http://"
  Fix: Reject non-HTTPS URLs: if !strings.HasPrefix(url, "https://") { return err }
```

## Example 8: Compare Reviews Across Commits

Track how code quality changes:

```bash
# Review original version
git checkout v4.15.0
./test/review.sh --range HEAD~5..HEAD --output review-v4.15.0.json

# Review current version
git checkout main
./test/review.sh --range HEAD~5..HEAD --output review-main.json

# Compare findings count
echo "v4.15.0 findings: $(jq '.findings | length' review-v4.15.0.json)"
echo "main findings: $(jq '.findings | length' review-main.json)"

# Compare severity distribution
jq -r '.findings | group_by(.severity) |
  map({severity: .[0].severity, count: length}) |
  .[] | "\(.severity): \(.count)"' review-main.json
```

## Example 9: Custom Filtering for CVO Components

Review only changes to specific CVO components:

```bash
# Review only CVO core operator changes
./test/review.sh --files $(git diff --name-only origin/main...HEAD | grep '^pkg/cvo/')

# Review only payload-related changes
./test/review.sh --files $(git diff --name-only origin/main...HEAD | grep '^pkg/payload/')

# Review only library changes
./test/review.sh --files $(git diff --name-only origin/main...HEAD | grep '^lib/')
```

## Example 10: Automated PR Comments

Post review findings as PR comments using `gh`:

```bash
#!/bin/bash
PR_NUMBER=$1

# Run review
./test/review.sh --pr "$PR_NUMBER" --output review.json

# Get findings
FINDINGS=$(jq -r '.findings[] |
  select(.severity=="high" or .severity=="medium") |
  "**[\(.severity | ascii_upcase)] \(.file):\(.line_or_range)**\n\n\(.message)\n\n\(.suggestion // "")\n"' review.json)

# Post as PR comment
gh pr comment "$PR_NUMBER" --body "## 🤖 Automated Code Review

$FINDINGS

---
_Generated by CVO code review agent_"
```

## Example 11: Review with Context from Logs

When reviewing a bug fix, include error logs as context:

```bash
# Suppose you're fixing a bug from a failing test
ERROR_LOG=$(oc logs -n openshift-cluster-version -l k8s-app=cluster-version-operator | tail -100)

# Create enhanced prompt
cat > /tmp/review-with-context.txt <<EOF
Review the following fix for a bug where CVO crashes during payload sync.

Error logs from failing cluster:
$ERROR_LOG

Code changes:
$(git diff origin/main...HEAD)

Focus your review on:
1. Does the fix address the root cause shown in logs?
2. Are there similar error paths that need the same fix?
3. Is there sufficient error handling to prevent crashes?
EOF

# Provide to Claude Code manually or via API
```

## Example 12: Batch Review Multiple PRs

Review a set of related PRs:

```bash
#!/bin/bash
PRS=(1270 1271 1272 1273)

for pr in "${PRS[@]}"; do
  echo "Reviewing PR #$pr..."
  ./test/review.sh --pr "$pr" --output "review-pr-${pr}.json"

  VERDICT=$(jq -r '.verdict' "review-pr-${pr}.json")
  echo "PR #$pr: $VERDICT"
done

# Generate summary report
echo "# Batch Review Summary" > batch-review.md
for pr in "${PRS[@]}"; do
  echo "## PR #$pr" >> batch-review.md
  jq -r '"**Verdict:** \(.verdict)\n\n**Top Risks:**\n\(.top_risks[] | "- \(.)")\n"' \
    "review-pr-${pr}.json" >> batch-review.md
done

cat batch-review.md
```

## Tips and Best Practices

### 1. Use Incremental Reviews
Don't wait until you have 50 files changed. Review frequently:
```bash
# After each logical change
git add -p  # Stage changes
./test/review.sh --files $(git diff --cached --name-only)
```

### 2. Combine with Linters
Code review agent complements but doesn't replace static analysis:
```bash
golangci-lint run ./... | tee lint-results.txt
./test/review.sh --output review.json

# Check both
echo "Lint issues: $(wc -l < lint-results.txt)"
echo "Review findings: $(jq '.findings | length' review.json)"
```

### 3. Focus on Your Changes
Use `--files` to review only what you touched:
```bash
MY_FILES=$(git diff --name-only origin/main...HEAD | grep '^pkg/cvo/')
./test/review.sh --files $MY_FILES
```

### 4. Archive Reviews
Keep historical reviews for reference:
```bash
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
./test/review.sh --output "reviews/review-${TIMESTAMP}.json"
git add "reviews/review-${TIMESTAMP}.json"
```

### 5. Review Before Committing
Make code review part of your pre-commit workflow:
```bash
# Stage changes
git add pkg/cvo/cvo.go

# Review staged changes
git diff --cached | ./test/review.sh --diff-file /dev/stdin

# If LGTM, commit
git commit -m "cvo: fix synchronization race condition"
```

## Troubleshooting Examples

### Large Diff Timeout
```bash
# Split by file
for file in $(git diff --name-only origin/main...HEAD); do
  echo "Reviewing $file..."
  git diff origin/main...HEAD -- "$file" > /tmp/single-file.diff
  ./test/review.sh --diff-file /tmp/single-file.diff --output "review-$(basename $file).json"
done
```

### No Changes Detected
```bash
# Check your diff
git diff origin/main...HEAD

# Update origin
git fetch origin

# Verify branch
git log --oneline origin/main..HEAD
```

### JSON Parse Errors
```bash
# Validate review output
jq empty review.json && echo "Valid JSON" || echo "Invalid JSON"

# Pretty-print for debugging
jq . review.json

# Extract just the summary if full JSON is malformed
grep -A 5 '"summary"' review.json
```

---

## Next Steps

- Read [CODE_REVIEW_AGENT.md](CODE_REVIEW_AGENT.md) for full documentation
- Customize [code-review-agent.json](code-review-agent.json) for your needs
- Integrate into your CI/CD pipeline
- Share feedback with the CVO team
