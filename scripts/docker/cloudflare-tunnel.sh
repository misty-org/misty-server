#!/bin/sh
set -eu

origin="${MISTY_TUNNEL_ORIGIN:-http://api:8081}"
runtime_dir=/run/misty
url_file="$runtime_dir/tunnel-url"
fifo=/tmp/misty-cloudflared-output

mkdir -p "$runtime_dir"
rm -f "$url_file" "$fifo"
mkfifo "$fifo"

cloudflared --no-autoupdate tunnel --url "$origin" >"$fifo" 2>&1 &
cloudflared_pid=$!

stop_cloudflared() {
  kill "$cloudflared_pid" 2>/dev/null || true
  wait "$cloudflared_pid" 2>/dev/null || true
}
trap stop_cloudflared INT TERM EXIT

while IFS= read -r line; do
  printf '%s\n' "$line"
  tunnel_url="$(printf '%s\n' "$line" | sed -n 's#.*\(https://[a-zA-Z0-9-]*\.trycloudflare\.com\).*#\1#p')"
  if [ -n "$tunnel_url" ] && [ ! -s "$url_file" ]; then
    printf '%s\n' "$tunnel_url" >"$url_file.tmp"
    chmod 0444 "$url_file.tmp"
    mv "$url_file.tmp" "$url_file"
    echo "Server running at ${tunnel_url%/}/api"
  fi
done <"$fifo"

wait "$cloudflared_pid"
