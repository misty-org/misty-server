#!/bin/sh
set -eu

case "${1:-startup}" in
  deploy)
    exec /usr/local/bin/misty-cloudflare-deploy
    ;;
  startup) ;;
  *) echo "Usage: misty-setup [startup|deploy]" >&2; exit 2 ;;
esac

step=initialization
trap 'code=$?; if [ "$code" -ne 0 ]; then echo "Setup failed during: $step" >&2; fi' EXIT

step="Misty migrations"
echo "Setup: $step"
goose up

step="application permissions"
echo "Setup: $step"
/usr/local/bin/misty-grant-app-role

step="workflow database"
echo "Setup: $step"
/usr/local/bin/misty-workflow-database

step="workflow migrations"
echo "Setup: $step"
node /opt/agent-runtime/node_modules/@workflow/world-postgres/bin/setup.js

step="collaboration configuration"
echo "Setup: $step"
/usr/local/bin/misty-cloudflare-init
echo "Server setup complete."
