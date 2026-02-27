# Review Script Updates - Hybrid gh/git Approach

## Summary

Updated [review.sh](review.sh) to use a **hybrid approach** for PR reviews:
1. **Preferred**: Use `gh` CLI if available (best experience)
2. **Fallback**: Use `git fetch` if `gh` not installed (no extra dependencies)

## What Changed

### 1. Smart Detection (Line 94)
```bash
if command -v gh &> /dev/null; then
  # Use gh CLI path
else
  # Use git fetch fallback
fi
```

### 2. Git Fallback Implementation (Lines 106-142)
When `gh` CLI is not available:
- Fetches PR using GitHub's pull request refs: `pull/{PR_NUMBER}/head`
- Auto-detects base branch from remote
- Supports custom remote via `PR_REMOTE` environment variable
- Provides helpful error messages with multiple solutions

### 3. New Environment Variable: `PR_REMOTE`
```bash
PR_REMOTE="${PR_REMOTE:-origin}"  # Default: origin, can override
```

Allows fetching PRs from different remotes (useful for forks):
```bash
PR_REMOTE=upstream ./test/review.sh --pr 1234
```

### 4. Improved Error Messages (Lines 120-131)
Now explains:
- Why fetch might fail (fork, closed PR, network)
- 4 different solutions to try
- How to use `PR_REMOTE` environment variable

### 5. Updated Usage Documentation
- Documents `PR_REMOTE` environment variable
- Explains gh CLI vs git fetch behavior
- Shows fork workflow examples

## Usage Examples

### With gh CLI (Recommended)
```bash
# Install gh first: https://cli.github.com/
gh auth login

# Review a PR
./test/review.sh --pr 1273
```

**Output:**
```
Fetching PR #1273...
Using 'gh' CLI to fetch PR...
Files changed:
pkg/cvo/cvo.go
pkg/payload/payload.go
...
```

### Without gh CLI (Git Fallback)

#### From Upstream Repository
```bash
# Works if 'origin' points to github.com/openshift/cluster-version-operator
./test/review.sh --pr 1273
```

**Output:**
```
Fetching PR #1273...
Note: 'gh' CLI not found. Using git fetch as fallback.
Install 'gh' for better PR integration: https://cli.github.com/

Fetching pull/1273/head from remote 'origin'...
Successfully fetched PR #1273
```

#### From a Fork
```bash
# Add upstream remote (one time)
git remote add upstream https://github.com/openshift/cluster-version-operator.git

# Fetch PR from upstream
PR_REMOTE=upstream ./test/review.sh --pr 1273
```

**Output:**
```
Fetching PR #1273...
Note: 'gh' CLI not found. Using git fetch as fallback.
Install 'gh' for better PR integration: https://cli.github.com/

Fetching pull/1273/head from remote 'upstream'...
Successfully fetched PR #1273
```

### Error Handling Example

If git fetch fails:
```
Error: Could not fetch PR #1273 from remote 'origin'

This can happen if:
  - Your 'origin' remote is a fork (not the upstream repo)
  - The PR doesn't exist or is closed
  - You don't have network access to the repository

Possible solutions:
  1. Install 'gh' CLI (recommended): https://cli.github.com/manual/installation
  2. Add upstream remote: git remote add upstream https://github.com/openshift/cluster-version-operator.git
     Then use: PR_REMOTE=upstream ./test/review.sh --pr 1273
  3. Manually fetch: git fetch origin pull/1273/head:pr-1273
  4. Use git range instead: ./test/review.sh --range origin/main...HEAD
```

## Comparison: gh CLI vs Git Fallback

| Feature | gh CLI | Git Fallback |
|---------|--------|--------------|
| **Installation** | Requires gh CLI | No extra tools needed |
| **Authentication** | Handles auth automatically | Uses git credentials |
| **PR Metadata** | Full metadata (title, author, etc.) | Limited to diff |
| **Private Repos** | ✅ Works with auth | ⚠️ Requires git access |
| **Fork Support** | ✅ Automatic | ⚠️ Needs `PR_REMOTE=upstream` |
| **Closed PRs** | ✅ Can fetch | ✅ Can fetch |
| **Error Messages** | Detailed | Basic |

## Recommendations

### For Individual Developers
**Install `gh` CLI** - Best experience, handles all edge cases:
```bash
# macOS
brew install gh

# Linux
sudo apt install gh  # Debian/Ubuntu
sudo dnf install gh  # Fedora/RHEL

# Authenticate
gh auth login
```

### For CI/CD Environments
- GitHub Actions: `gh` is pre-installed ✅
- GitLab CI: Use git fallback or install `gh`
- Prow Jobs: Typically has `gh` available

### For Fork Workflows
```bash
# One-time setup
git remote add upstream https://github.com/openshift/cluster-version-operator.git

# Create shell alias for convenience
alias review-pr='PR_REMOTE=upstream ./test/review.sh --pr'

# Use it
review-pr 1273
```

## Testing the Changes

### Test gh CLI Path
```bash
# Requires gh CLI installed
./test/review.sh --pr 1273
```

### Test Git Fallback Path
```bash
# Temporarily hide gh CLI
alias gh='echo "gh not found" && false'

# Run review
./test/review.sh --pr 1273

# Remove alias
unalias gh
```

### Test Custom Remote
```bash
git remote add upstream https://github.com/openshift/cluster-version-operator.git
PR_REMOTE=upstream ./test/review.sh --pr 1273
```

## Files Modified

1. **[review.sh](review.sh)** - Main script
   - Lines 17-18: Added `PR_REMOTE` variable
   - Lines 23-45: Updated usage documentation
   - Lines 91-142: Hybrid gh/git implementation
   - Error handling and messaging improvements

## Benefits

1. **No Hard Dependency**: Works without `gh` CLI
2. **Better UX**: Tries `gh` first, falls back gracefully
3. **Fork Support**: `PR_REMOTE` handles fork workflows
4. **Clear Errors**: Helpful messages with actionable solutions
5. **Flexible**: Multiple ways to accomplish the same goal

## Future Enhancements

Potential improvements for future versions:
- Cache fetched PRs to avoid re-fetching
- Support PR URLs in addition to numbers
- Add `--pr-remote` flag as alternative to env var
- Integrate with Claude Code's native PR review features
