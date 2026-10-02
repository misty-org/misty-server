# The build stage runs natively on the build machine and cross-compiles for the
# target platform, so an amd64 image builds at full speed on Apple Silicon.
FROM --platform=$BUILDPLATFORM golang:1.25.12-bookworm@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58 AS builder
ARG TARGETOS TARGETARCH
ENV GOOSE_TAGS="no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb"

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
# One goose for this machine (to validate migrations) and one for the image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOMAXPROCS=2 CGO_ENABLED=0 GOBIN=/host go install -p 2 -tags="$GOOSE_TAGS" github.com/pressly/goose/v3/cmd/goose@v3.27.3 \
    && GOMAXPROCS=2 CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go install -p 2 -tags="$GOOSE_TAGS" github.com/pressly/goose/v3/cmd/goose@v3.27.3 \
    && mkdir -p /out \
    && { cp "$(go env GOPATH)/bin/${TARGETOS}_${TARGETARCH}/goose" /out/goose 2>/dev/null \
         || cp "$(go env GOPATH)/bin/goose" /out/goose; }
COPY . .
RUN /host/goose -dir internal/platform/postgres/migrations validate
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build GOMAXPROCS=2 CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -p 2 \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/misty-server ./cmd/misty-server

FROM debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin misty \
    && mkdir -p /var/lib/misty/library \
    && chown -R 10001:10001 /var/lib/misty

COPY --from=builder --chown=10001:10001 /out/misty-server /usr/local/bin/misty-server
COPY --from=builder --chown=10001:10001 /out/goose /usr/local/bin/goose
COPY --chown=10001:10001 internal/platform/postgres/migrations /app/migrations
COPY --chmod=0555 scripts/docker/api-entrypoint.sh /usr/local/bin/misty-dev-api-entrypoint

USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD curl --fail --silent --show-error http://127.0.0.1:${PORT:-8080}/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/misty-server"]
