#!/bin/sh
set -eu

if [ -z "${STRIPE_API_KEY:-}" ]; then
  echo "STRIPE_SECRET_KEY is required to start Stripe event forwarding." >&2
  exit 1
fi

forward_to="${STRIPE_FORWARD_TO:-http://api:8081/stripe/webhook}"
secret_file=/run/misty/stripe-webhook-secret

rm -f "$secret_file"
webhook_secret="$(stripe listen --api-key "$STRIPE_API_KEY" --print-secret | tr -d '\r\n')"
case "$webhook_secret" in
  whsec_*) ;;
  *)
    echo "Stripe CLI did not return a valid webhook signing secret." >&2
    exit 1
    ;;
esac

printf '%s\n' "$webhook_secret" >"$secret_file.tmp"
chmod 0444 "$secret_file.tmp"
mv "$secret_file.tmp" "$secret_file"

echo "Stripe events will be forwarded to $forward_to"
exec stripe listen --api-key "$STRIPE_API_KEY" --forward-to "$forward_to"
