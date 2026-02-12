#!/usr/bin/env bash
#
# Helper script to run code review using Gemini
#
# Usage:
#   ./hack/review.sh                    # Review current branch vs origin/main
#   ./hack/review.sh --pr 1234          # Review specific PR
#   ./hack/review.sh --files file1.go file2.go  # Review specific files

# set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${SCRIPT_DIR}"

# Default values
PR_NUMBER=""
PR_REMOTE="${PR_REMOTE:-origin}"  # Can be overridden via env var (e.g., PR_REMOTE=upstream)

# Determine the default branch from the remote
DEFAULT_BRANCH=$(git remote show "${PR_REMOTE}" 2>/dev/null | grep 'HEAD branch' | cut -d' ' -f5)
if [[ -z "$DEFAULT_BRANCH" ]]; then
    # Fallback to main if remote is not configured or branch not found
    DEFAULT_BRANCH="main"
fi
DIFF_RANGE="${PR_REMOTE}/${DEFAULT_BRANCH}...HEAD"

FILES=()

# Pre-parse arguments to handle --files
for arg in "$@"; do
  case "$arg" in
    -f|--files)
      # When reviewing specific files,
      # default to showing local changes against HEAD instead of a branch diff.
      DIFF_RANGE="HEAD"
      break
      ;;
  esac
done


function usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Run automated code review using Gemini agent.

Options:
  -p, --pr NUMBER         Review a specific pull request number
  -f, --files FILE...     Review only specific files
  -h, --help              Show this help message

Environment Variables:
  PR_REMOTE           Git remote to use for fetching PRs (default: origin)
                      Set to 'upstream' if working from a fork

Examples:
  $0                              # Review current changes vs main
  $0 --pr 1234                    # Review PR #1234 (uses gh CLI if available)
  PR_REMOTE=upstream $0 --pr 1234 # Review PR from upstream remote
  $0 --files pkg/cvo/cvo.go       # Review specific file

Notes:
  - PR reviews work best with 'gh' CLI installed: https://cli.github.com/
  - Without 'gh', falls back to git fetch (requires proper remote configuration)
  - When working from a fork, use PR_REMOTE=upstream to fetch from main repository
EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    -p|--pr)
      PR_NUMBER="$2"
      shift 2
      ;;
    -f|--files)
      shift
      # stip options until next flag or end of args
      while [[ $# -gt 0 ]] && [[ ! $1 =~ ^- ]]; do
        if [[ ! $1 =~ ^vendor/ ]]; then
          FILES+=("$1")
        fi
        shift
      done
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      usage
      exit 1
      ;;
  esac
done

cd "$REPO_ROOT" || exit 0

# Get diff based on input
if [[ -n "$PR_NUMBER" ]]; then
  PR_REF="pull/${PR_NUMBER}/head"
  FETCH_SUCCESS="false"
  
  # Find the correct remote that has the PR ref by trying all of them.
  for remote in $(git remote); do
      echo "Attempting to fetch PR #${PR_NUMBER} from remote '$remote'..."
      if git fetch "$remote" "$PR_REF" 2>/dev/null; then
          echo "Successfully fetched PR from remote '$remote'."
          PR_REMOTE=$remote # Set the successful remote for the diff operation below
          FETCH_SUCCESS="true"
          break
      fi
  done

  if [[ "$FETCH_SUCCESS" == "false" ]]; then
    echo ""
    echo "Error: Could not fetch PR #${PR_NUMBER} from any of your configured remotes."
    echo "Attempted remotes: $(git remote | tr '\n' ' ')"
    echo ""
    echo "This can happen if:"
    echo "  - You are working on a fork and have not configured a remote for the upstream repository."
    echo "  - The PR doesn't exist or is closed."
    echo "  - You don't have network access to the repository."
    echo ""
    echo "Possible solutions:"
    echo "  1. Add the main repository as a remote. e.g.:"
    echo "     git remote add upstream https://github.com/openshift/cluster-version-operator.git"
    echo "  2. Manually fetch the PR into a local branch."
    exit 1
  fi

  # Determine the base branch for the diff
  echo "Determining base branch for comparison..."
  # First, update the remote's HEAD branch information locally
  git remote set-head "$PR_REMOTE" --auto &>/dev/null

  # Now, get the HEAD branch name from the updated remote info
  BASE_BRANCH=$(git remote show "$PR_REMOTE" 2>/dev/null | grep 'HEAD branch' | cut -d' ' -f5)
  # Fallback to master for this specific repository if lookup fails
  BASE_BRANCH=${BASE_BRANCH:-master}
  echo "Base branch identified as: ${PR_REMOTE}/${BASE_BRANCH}"

  DIFF=$(git diff "${PR_REMOTE}/${BASE_BRANCH}...FETCH_HEAD" -- . ':(exclude)vendor/*')
  FILES_CHANGED=$(git diff --name-only "${PR_REMOTE}/${BASE_BRANCH}...FETCH_HEAD" -- . ':(exclude)vendor/*')

  echo "Successfully generated diff for PR #${PR_NUMBER}."

elif [[ ${#FILES[@]} -gt 0 ]]; then
  echo "Reviewing specified files: ${FILES[*]}"
  DIFF=$(git diff "$DIFF_RANGE" -- "${FILES[@]}")
  FILES_CHANGED=$(printf '%s
' "${FILES[@]}")
else
  echo "Reviewing changes in range: $DIFF_RANGE"
  DIFF=$(git diff "$DIFF_RANGE" -- . ':(exclude)vendor/*')
  FILES_CHANGED=$(git diff --name-only "$DIFF_RANGE" -- . ':(exclude)vendor/*')
fi

if [[ -z "$DIFF" ]]; then
  echo "No changes found to review."
  exit 0
fi

echo "Files changed:"
echo "$FILES_CHANGED"
echo ""
echo "Generating code review..."
echo ""

# Create prompt for the agent
REPO_NAME=$(git remote get-url origin | sed -e 's/.*[:/]\([^/]*\/[^/]*\)\.git$/\1/' -e 's/.*[:/]\([^/]*\/[^/]*\)$/\1/')

REVIEW_PROMPT=$(cat <<REVIEW_PROMPT_EOF
Please perform a code review for the following changes in the ${REPO_NAME} repository.

As a reviewer, you must follow these instructions:
1.  **Analyze the provided git diff.** The user has provided the diff of the changes.
2.  **Apply Go best practices.** Refer to "Effective Go" for guidance on idiomatic Go, including formatting, naming conventions, and.
3.  **Consider oc-specific conventions.** Since 'oc' is built on 'kubectl', ensure that kubectl compatibility is maintained, OpenShift-specific commands follow existing patterns, and CLI output is consistent.
4.  **Check for code quality issues.** Look for potential race conditions, resource leaks, and proper context propagation.
5.  **Verify documentation.** Ensure that exported functions and types have doc comments, and complex logic is explained.
6.  **Provide a structured summary of your findings.** Organize your feedback clearly.

Files changed:
${FILES_CHANGED}

Git diff:
${DIFF}

Provide your code review based on the instructions above.
REVIEW_PROMPT_EOF
)

# Save prompt to temp file for gemini CLI
PROMPT_FILE=$(mktemp)
echo "$REVIEW_PROMPT" > "$PROMPT_FILE"

echo "=== CODE REVIEW RESULTS ==="
echo ""

# Check if gemini CLI is available
if command -v gemini &> /dev/null; then
  # Use Gemini CLI if available
  gemini -y "$(cat "$PROMPT_FILE")"
else
  # Fallback: Print instructions for manual review
  echo "gemini CLI not found."
  echo ""
  echo "To perform the review, copy the following prompt to Gemini:"
  echo "---------------------------------------------------------------"
  cat "$PROMPT_FILE"
  echo "---------------------------------------------------------------"
fi

rm -f "$PROMPT_FILE"
