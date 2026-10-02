#!/usr/bin/env bash
# Builds the production images on this computer (nothing builds on GitHub),
# pushes them to GHCR, tags the commit, and writes the image digests to
# $MISTY_RELEASE_OUTPUT for the CLI to save in server/.env/prod/runtime.env.
# Usage (through the CLI): misty server release 0.1.0
set -euo pipefail
version="${1:?Name the release version, such as 0.1.0}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || { echo "Versions look like 0.1.0." >&2; exit 1; }
: "${MISTY_RELEASE_OUTPUT:?run through misty server release}"
root=$(git rev-parse --show-toplevel)
cd "$root"
tag="server-v$version"

git diff --quiet && git diff --cached --quiet || { echo "Commit or discard local changes first." >&2; exit 1; }
git fetch --quiet origin
[ "$(git rev-parse HEAD)" = "$(git rev-parse '@{upstream}')" ] ||
  { echo "Push this commit first, so every release is a commit on GitHub." >&2; exit 1; }
! git rev-parse -q --verify "refs/tags/$tag" >/dev/null || { echo "$tag already exists." >&2; exit 1; }
docker buildx version >/dev/null

echo "Running the server suites on $(git rev-parse --short HEAD)"
.githooks/checks.sh secrets
.githooks/checks.sh server

sha=$(git rev-parse HEAD)
metadata=$(mktemp -d)
trap 'rm -rf "$metadata"' EXIT
build() { # name dockerfile [target]; prints image@digest on stdout
  local image="ghcr.io/misty-org/$1"
  echo "Building $image:$version for linux/amd64" >&2
  docker buildx build --platform linux/amd64 --provenance=false --push \
    --file "$2" ${3:+--target "$3"} \
    --tag "$image:$version" --tag "$image:sha-$sha" \
    --metadata-file "$metadata/$1.json" server >&2
  printf '%s@%s' "$image" "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["containerimage.digest"])' "$metadata/$1.json")"
}
api=$(build misty-api server/Dockerfile)
agent=$(build misty-agent-runtime server/apps/agent-runtime/Dockerfile runtime)

git tag -a "$tag" -m "Misty server $version" "$sha"
git push --quiet --no-verify origin "$tag"   # the commit was already checked above
printf 'MISTY_API_IMAGE=%s\nMISTY_AGENT_RUNTIME_IMAGE=%s\n' "$api" "$agent" > "$MISTY_RELEASE_OUTPUT"
echo "Released $tag."
