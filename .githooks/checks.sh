#!/usr/bin/env bash
# Local checks for every commit and push (there is no GitHub CI).
#   .githooks/checks.sh commit   suites touched by the staged changes
#   .githooks/checks.sh push     every suite and the secret scan, on a clean tree
#   .githooks/checks.sh go|collab|secrets
# Skip once with git commit/push --no-verify.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
cd "$root"
# Git exports these to hooks; tests that create repositories must not inherit them.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES
hooks="$root/.githooks"

go_suite() {
  bash "$root/scripts/check-go-sizes.sh"
  bash "$hooks/server-tests.sh"
}
collab() {
  (
    cd services/journal-collab
    # Reinstalling takes longer than the tests, so only when the lockfile moved.
    [ node_modules/.package-lock.json -nt package-lock.json ] || npm ci --no-audit --no-fund --silent
    npm run typecheck && npm test && npm run test:runtime
  )
}

gitleaks_image=zricethezav/gitleaks@sha256:e1b35e12a8c6fa8901f060459cfb6b2fc4c484d3afbe3b029733a3bbfab07055
leaks() {
  docker run --rm -v "$root:/repo:ro" -w /repo -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory \
    -e GIT_CONFIG_VALUE_0=/repo "$gitleaks_image" "$@" --source=/repo --redact --config=/repo/.gitleaks.toml
}
# Scans commits, never the working folder: git-ignored .env files stay out.
secrets() {
  local range="HEAD --not --remotes"
  if upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null); then
    range="$upstream..HEAD"
  fi
  # shellcheck disable=SC2086 # the range is several git arguments
  [ -z "$(git rev-list $range 2>/dev/null | head -n 1)" ] && { echo "No new commits to scan."; return 0; }
  leaks detect --log-opts="$range"
}

run() {
  local name=$1 started=$SECONDS
  echo "── $name"
  "$name"
  echo "✓ $name ($((SECONDS - started))s)"
}

case "${1:-}" in
  commit)
    # Tests must see the same tracked files as the proposed commit.
    if ! git diff --quiet || [ -n "$(git ls-files --others --exclude-standard)" ]; then
      echo "Stage or stash outstanding files before committing so tests match the staged snapshot." >&2
      exit 1
    fi
    leaks protect --staged
    staged=$(git diff --cached --name-only --diff-filter=ACMRD)
    if echo "$staged" | grep -qE '^services/journal-collab/'; then run collab; fi
    if echo "$staged" | grep -qE '\.go$|^go\.(mod|sum)$|^scripts/|^\.githooks/'; then run go_suite; fi
    ;;
  push)
    # Tests run on the working tree, so it must match what is being pushed.
    if ! git diff --quiet || ! git diff --cached --quiet || [ -n "$(git ls-files --others --exclude-standard)" ]; then
      echo "Commit or discard local changes before pushing; checks must run on the pushed commit." >&2
      exit 1
    fi
    run secrets
    run go_suite
    run collab
    ;;
  go) run go_suite ;;
  collab | secrets) run "$1" ;;
  *) echo "Usage: .githooks/checks.sh commit|push|go|collab|secrets" >&2; exit 2 ;;
esac
