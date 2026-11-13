#!/bin/sh
set -eu

runtime_dir=/run/misty

mkdir -p "$runtime_dir"
rm -f "$runtime_dir/journal-server.env"

if [ -z "${JOURNAL_COLLAB_TICKET_PRIVATE_KEY:-}" ] ||
  [ -z "${JOURNAL_COLLAB_CONTROL_SECRET:-}" ] ||
  [ -z "${JOURNAL_COLLAB_PROJECTION_SECRET:-}" ]; then
  echo "Journal collaboration secrets are missing or only partially configured." >&2
  echo "Run: misty setup server" >&2
  exit 1
fi

{
  printf 'JOURNAL_COLLAB_TICKET_PRIVATE_KEY=%s\n' "$JOURNAL_COLLAB_TICKET_PRIVATE_KEY"
  printf 'JOURNAL_COLLAB_CONTROL_SECRET=%s\n' "$JOURNAL_COLLAB_CONTROL_SECRET"
  printf 'JOURNAL_COLLAB_PROJECTION_SECRET=%s\n' "$JOURNAL_COLLAB_PROJECTION_SECRET"
} >"$runtime_dir/journal-server.env"
chmod 0444 "$runtime_dir/journal-server.env"

echo "Journal collaboration secrets are ready."
