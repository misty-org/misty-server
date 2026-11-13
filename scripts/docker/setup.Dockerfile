# Compose supplies the same API image the running service uses.
FROM api AS api

# The setup command (cmd/misty-dev-setup), built natively and cross-compiled.
FROM --platform=$BUILDPLATFORM golang:1.25.12-bookworm@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58 AS tools
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/misty-dev-setup ./cmd/misty-dev-setup
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/misty-setup ./cmd/misty-dev-setup

FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd

COPY --from=api /usr/local/bin/goose /usr/local/bin/goose
COPY --from=api /app/migrations /app/migrations

WORKDIR /app
COPY services/journal-collab/package.json services/journal-collab/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY services/journal-collab/tsconfig.json services/journal-collab/wrangler.jsonc ./
COPY services/journal-collab/src ./src
COPY --from=tools /out/misty-setup /usr/local/bin/misty-setup

ENTRYPOINT ["/usr/local/bin/misty-setup"]
CMD ["startup"]
