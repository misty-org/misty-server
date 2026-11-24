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

FROM alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1

COPY --from=api /usr/local/bin/goose /usr/local/bin/goose
COPY --from=api /app/migrations /app/migrations
COPY --from=tools /out/misty-setup /usr/local/bin/misty-setup

ENTRYPOINT ["/usr/local/bin/misty-setup"]
CMD ["startup"]
