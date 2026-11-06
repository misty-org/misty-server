# The Misty tools image: the setup, migrate, backups, alerts, console and tunnel
# services of compose.yml all run it. Build context: the repository root.

# Bundle the Worker with wrangler's dry run; the image ships the bundle so
# setup deploys them through the Cloudflare API with no Node at run time.
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS workers
ENV WRANGLER_SEND_METRICS=false CI=true
WORKDIR /services/journal-collab
COPY services/journal-collab/package.json services/journal-collab/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY services/journal-collab/tsconfig.json services/journal-collab/wrangler.jsonc ./
COPY services/journal-collab/src ./src
RUN npx wrangler deploy --dry-run --outdir dist

FROM --platform=$BUILDPLATFORM golang:1.25.12-bookworm@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58 AS tools
ARG TARGETOS TARGETARCH
ENV GOOSE_TAGS="no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb" \
    CGO_ENABLED=0 GOMAXPROCS=2
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go install -p 2 -tags="$GOOSE_TAGS" github.com/pressly/goose/v3/cmd/goose@v3.27.3 \
    && mkdir -p /out \
    && { cp "$(go env GOPATH)/bin/${TARGETOS}_${TARGETARCH}/goose" /out/goose 2>/dev/null \
         || cp "$(go env GOPATH)/bin/goose" /out/goose; }
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -p 2 -trimpath -ldflags="-s -w" -o /out/ ./deploy/setup ./cmd/misty-console

FROM cloudflare/cloudflared:2026.7.3@sha256:e39ee8da81ad5e05d77f38d2f51c60ca51bf2a8450ac3abab50c17fdb91d91bf AS cloudflared

FROM alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1
RUN apk add --no-cache ca-certificates age postgresql16-client
COPY --from=cloudflared /usr/local/bin/cloudflared /usr/local/bin/cloudflared
COPY --from=tools /out/goose /usr/local/bin/goose
COPY --from=tools /out/setup /usr/local/bin/misty-setup
COPY --from=tools /out/misty-console /usr/local/bin/misty-console
COPY internal/platform/db/migrations /opt/misty/migrations
COPY --from=workers /services/journal-collab/wrangler.jsonc /opt/misty/workers/journal-collab/wrangler.jsonc
COPY --from=workers /services/journal-collab/dist /opt/misty/workers/journal-collab/dist
ENTRYPOINT ["/usr/local/bin/misty-setup"]
CMD ["init"]
