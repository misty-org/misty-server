#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

hard_max=500
preferred_max=300
failed=false
reported=false

while IFS= read -r -d '' file; do
  if head -n 10 "$file" | grep -Eq '^// Code generated .* DO NOT EDIT\\.$'; then
    continue
  fi

  line_count="$(wc -l < "$file" | tr -d '[:space:]')"
  if (( line_count > hard_max )); then
    printf 'ERROR %4d lines  %s (hard maximum: %d)\n' "$line_count" "$file" "$hard_max"
    failed=true
    continue
  fi
  if (( line_count > preferred_max )); then
    printf 'REPORT %3d lines  %s (preferred maximum: %d)\n' "$line_count" "$file" "$preferred_max"
    reported=true
  fi
done < <(
  find . -type f -name '*.go' \
    -not -path './.git/*' \
    -not -path './vendor/*' \
    -not -path '*/node_modules/*' \
    -print0 | sort -z
)

if [[ "$reported" == false && "$failed" == false ]]; then
  echo "All handwritten Go files are at most ${preferred_max} lines."
fi

if [[ "$failed" == true ]]; then
  exit 1
fi
