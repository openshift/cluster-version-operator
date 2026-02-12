#!/usr/bin/env bash
#
# Helper script to run code review using Claude Code's Task tool
#
# Usage:
#   ./hack/review.sh                    # Review current branch vs origin/main
#   ./hack/review.sh --pr 1234          # Review specific PR
#   ./hack/review.sh --range HEAD~3     # Review last 3 commits
#   ./hack/review.sh --files file1.go file2.go  # Review specific files

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Default values
DIFF_RANGE="origin/main...HEAD"
PR_NUMBER=""
PR_REMOTE="${PR_REMOTE:-origin}"  # Can be overridden via env var (e.g., PR_REMOTE=upstream)
FILES=()
OUTPUT_FILE="${REPO_ROOT}/review-output.json"

function usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Run automated code review using Claude Code agent.

Options:
  --pr NUMBER         Review a specific pull request number
  --range RANGE       Git diff range (default: origin/main...HEAD)
  --files FILE...     Review only specific files
  --output FILE       Output file for review results (default: ${REPO_ROOT}/review-output.json)
  -h, --help          Show this help message

Environment Variables:
  PR_REMOTE           Git remote to use for fetching PRs (default: origin)
                      Set to 'upstream' if working from a fork

Examples:
  $0                              # Review current changes vs main
  $0 --pr 1234                    # Review PR #1234 (uses gh CLI if available)
  PR_REMOTE=upstream $0 --pr 1234 # Review PR from upstream remote
  $0 --range HEAD~5..HEAD         # Review last 5 commits
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
    --pr)
      PR_NUMBER="$2"
      shift 2
      ;;
    --range)
      DIFF_RANGE="$2"
      shift 2
      ;;
    --files)
      shift
      while [[ $# -gt 0 ]] && [[ ! $1 =~ ^-- ]]; do
        FILES+=("$1")
        shift
      done
      ;;
    --output)
      OUTPUT_FILE="$2"
      shift 2
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

cd "$REPO_ROOT"

# Get diff based on input
if [[ -n "$PR_NUMBER" ]]; then
  echo "Fetching PR #${PR_NUMBER}..."

  if command -v gh &> /dev/null; then
    # Use gh CLI (preferred - shows PR metadata and handles authentication)
    echo "Using 'gh' CLI to fetch PR..."
    DIFF=$(gh pr diff "$PR_NUMBER" 2>/tmp/gh_err_$$) || {
      echo "Error: Failed to fetch PR #${PR_NUMBER} using 'gh' CLI"
      echo "Output: $(cat /tmp/gh_err_$$)"
      rm -f /tmp/gh_err_$$
      exit 1
    }
    rm -f /tmp/gh_err_$$
    FILES_CHANGED=$(gh pr view "$PR_NUMBER" --json files -q '.files[].path') || {
      echo "Error: Failed to get PR file list"
      exit 1
    }
  else
    # Fallback to git fetch (no gh CLI required)
    # Note: This requires 'origin' to be the upstream GitHub repo, not a fork
    echo "Note: 'gh' CLI not found. Using git fetch as fallback."
    echo "Install 'gh' for better PR integration: https://cli.github.com/"
    echo ""

    PR_REF="pull/${PR_NUMBER}/head"
    echo "Fetching ${PR_REF} from remote '${PR_REMOTE}'..."

    if ! git fetch "$PR_REMOTE" "$PR_REF" 2>/dev/null; then
      echo ""
      echo "Error: Could not fetch PR #${PR_NUMBER} from remote '${PR_REMOTE}'"
      echo ""
      echo "This can happen if:"
      echo "  - Your '${PR_REMOTE}' remote is a fork (not the upstream repo)"
      echo "  - The PR doesn't exist or is closed"
      echo "  - You don't have network access to the repository"
      echo ""
      echo "Possible solutions:"
      echo "  1. Install 'gh' CLI (recommended): https://cli.github.com/manual/installation"
      echo "  2. Add upstream remote: git remote add upstream https://github.com/openshift/cluster-version-operator.git"
      echo "     Then use: PR_REMOTE=upstream ./hack/review.sh --pr ${PR_NUMBER}"
      echo "  3. Manually fetch: git fetch origin pull/${PR_NUMBER}/head:pr-${PR_NUMBER}"
      echo "  4. Use git range instead: ./hack/review.sh --range origin/main...HEAD"
      exit 1
    fi

    # Get the base branch (usually main)
    BASE_BRANCH=$(git remote show "$PR_REMOTE" 2>/dev/null | grep 'HEAD branch' | cut -d' ' -f5)
    BASE_BRANCH=${BASE_BRANCH:-main}

    DIFF=$(git diff "${PR_REMOTE}/${BASE_BRANCH}...FETCH_HEAD")
    FILES_CHANGED=$(git diff --name-only "${PR_REMOTE}/${BASE_BRANCH}...FETCH_HEAD")

    echo "Successfully fetched PR #${PR_NUMBER}"
  fi
elif [[ ${#FILES[@]} -gt 0 ]]; then
  echo "Reviewing specified files: ${FILES[*]}"
  DIFF=$(git diff "$DIFF_RANGE" -- "${FILES[@]}")
  FILES_CHANGED=$(printf '%s\n' "${FILES[@]}")
else
  echo "Reviewing changes in range: $DIFF_RANGE"
  DIFF=$(git diff "$DIFF_RANGE")
  FILES_CHANGED=$(git diff --name-only "$DIFF_RANGE")
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

Files changed:
${FILES_CHANGED}

Git diff:
\`\`\`diff
${DIFF}
\`\`\`

Analyze these changes according to the code review checklist and provide your findings in JSON format as specified in your instructions.
REVIEW_PROMPT_EOF
)

# Save prompt to temp file for claude CLI
PROMPT_FILE=$(mktemp)
echo "$REVIEW_PROMPT" > "$PROMPT_FILE"

echo "=== CODE REVIEW RESULTS ==="
echo ""

# Check if claude CLI is available
if command -v claude &> /dev/null; then
  # Use Claude Code CLI if available
  claude task --type general-purpose --prompt "@${PROMPT_FILE}" --output "$OUTPUT_FILE"
  echo ""
  echo "Review saved to: $OUTPUT_FILE"

  # Pretty print if jq is available
  if command -v jq &> /dev/null && [[ -f "$OUTPUT_FILE" ]]; then
    echo ""
    echo "=== SUMMARY ==="
    jq -r '.summary // "No summary available"' "$OUTPUT_FILE"
    echo ""
    echo "Verdict: $(jq -r '.verdict // "Unknown"' "$OUTPUT_FILE")"
    echo ""
    echo "Top Risks:"
    jq -r '.top_risks[]? // "None identified"' "$OUTPUT_FILE" | sed 's/^/  - /'
  fi
else
  # Fallback: Print instructions for manual review
  echo "Claude Code CLI not found."
  echo ""
  echo "To perform the review, copy the following prompt to Claude Code:"
  echo "---------------------------------------------------------------"
  cat "$PROMPT_FILE"
  echo "---------------------------------------------------------------"
fi

rm -f "$PROMPT_FILE"
