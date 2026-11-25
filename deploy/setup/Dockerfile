# The Misty tools image: the setup, migrate, backups, alerts and tunnel services
# of compose.yml all run it. Build context: the repository root.

FROM --platform=$BUILDPLATFORM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS tools
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
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -p 2 -trimpath -ldflags="-s -w" -o /out/ ./deploy/setup

FROM cloudflare/cloudflared:2026.7.3@sha256:e39ee8da81ad5e05d77f38d2f51c60ca51bf2a8450ac3abab50c17fdb91d91bf AS cloudflared

FROM alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1
RUN apk add --no-cache ca-certificates age postgresql16-client
COPY --from=cloudflared /usr/local/bin/cloudflared /usr/local/bin/cloudflared
COPY --from=tools /out/goose /usr/local/bin/goose
COPY --from=tools /out/setup /usr/local/bin/misty-setup
COPY internal/platform/db/migrations /opt/misty/migrations
ENTRYPOINT ["/usr/local/bin/misty-setup"]
CMD ["init"]
