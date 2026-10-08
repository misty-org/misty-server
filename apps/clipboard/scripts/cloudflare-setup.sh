#!/bin/sh
# Creates the clipboard's R2 bucket with a one-day lifecycle rule and deploys
# the Worker. Needs CLOUDFLARE_API_TOKEN (Workers Scripts and R2 edit) and the
# ticket public key the Misty API signs with.
#
#   CLIPBOARD_TICKET_PUBLIC_KEY=<base64 raw Ed25519 key> scripts/cloudflare-setup.sh
set -eu

cd "$(dirname "$0")/.."

: "${CLOUDFLARE_API_TOKEN:?CLOUDFLARE_API_TOKEN is required}"
: "${CLIPBOARD_TICKET_PUBLIC_KEY:?CLIPBOARD_TICKET_PUBLIC_KEY is required}"
bucket="${MISTY_CLIPBOARD_BUCKET:-misty-clipboard}"

if ! npx wrangler r2 bucket info "$bucket" >/dev/null 2>&1; then
  npx wrangler r2 bucket create "$bucket"
fi
# Clips last 24 hours; R2 removes their ciphertext a day after upload even if a
# room never deletes it.
npx wrangler r2 bucket lifecycle add "$bucket" expire-clips --expire-days 1 --force

printf '%s' "$CLIPBOARD_TICKET_PUBLIC_KEY" | npx wrangler secret put CLIPBOARD_TICKET_PUBLIC_KEY
if [ -n "${CLIPBOARD_TICKET_PUBLIC_KEY_PREVIOUS:-}" ]; then
  printf '%s' "$CLIPBOARD_TICKET_PUBLIC_KEY_PREVIOUS" | npx wrangler secret put CLIPBOARD_TICKET_PUBLIC_KEY_PREVIOUS
fi
npx wrangler deploy
