#!/bin/sh
set -eu

project_dir=/workspace
runtime_dir=/run/misty
dev_vars="$project_dir/.dev.vars"
server_env="$project_dir/.secrets/server.env"

mkdir -p "$runtime_dir"
rm -f "$runtime_dir/journal-server.env"

if [ ! -s "$dev_vars" ] || [ ! -s "$server_env" ]; then
  echo "Journal collaboration secrets are missing or only partially configured." >&2
  echo "Run: misty server worker generate-secrets" >&2
  exit 1
fi

cp "$server_env" "$runtime_dir/journal-server.env"
chmod 0444 "$runtime_dir/journal-server.env"

echo "Journal collaboration secrets are ready."
