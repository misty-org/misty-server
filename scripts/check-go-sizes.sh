#!/usr/bin/env bash
# Handwritten Go files should stay at 300 lines or fewer and must not pass 500.
# Generated files are skipped. `misty check go-sizes` applies the same limits.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
preferred=300
hard=500
failed=0
while IFS= read -r file; do
  if head -n 10 "$file" | tr -d '\r' | grep -qE '^// Code generated .* DO NOT EDIT\.$'; then continue; fi
  count=$(awk 'END { print NR }' "$file")
  if [ "$count" -gt "$hard" ]; then
    echo "ERROR $count lines  $file (hard maximum: $hard)"
    failed=1
  elif [ "$count" -gt "$preferred" ]; then
    echo "REPORT $count lines  $file (preferred maximum: $preferred)"
  fi
done < <(find . -name '*.go' -not -path './vendor/*' -not -path '*/node_modules/*' -not -path './.git/*' | sort)
exit "$failed"
