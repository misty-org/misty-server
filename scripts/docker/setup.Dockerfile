# Compose supplies the same API and agent images used by the running services.
FROM api AS api
FROM agent AS agent

FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd
RUN apk add --no-cache postgresql-client

COPY --from=api /usr/local/bin/goose /usr/local/bin/goose
COPY --from=api /app/migrations /app/migrations
COPY --from=agent /app/node_modules /opt/agent-runtime/node_modules

WORKDIR /app
COPY apps/journal-collab/package.json apps/journal-collab/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY apps/journal-collab/tsconfig.json apps/journal-collab/wrangler.jsonc ./
COPY apps/journal-collab/src ./src
COPY --chmod=0555 apps/journal-collab/docker/cloudflare-init.sh /usr/local/bin/misty-cloudflare-init
COPY --chmod=0555 apps/journal-collab/docker/cloudflare-deploy.sh /usr/local/bin/misty-cloudflare-deploy
COPY --chmod=0555 scripts/docker/postgres-grant-app-role.sh /usr/local/bin/misty-grant-app-role
COPY --chmod=0555 scripts/docker/setup.sh /usr/local/bin/misty-setup

ENTRYPOINT ["/usr/local/bin/misty-setup"]
CMD ["startup"]
