FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS builder
WORKDIR /src/apps/agent-runtime
COPY apps/agent-runtime/package.json apps/agent-runtime/package-lock.json ./
COPY apps/agent-runtime/vendor ./vendor
COPY apps/agent-runtime/scripts ./scripts
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY apps/agent-runtime ./
RUN npm run build

FROM builder AS setup
CMD ["npm", "run", "world:setup"]

FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS runtime
WORKDIR /app
ENV NODE_ENV=production PORT=3000
COPY --from=builder /src/apps/agent-runtime/node_modules ./node_modules
COPY --from=builder /src/apps/agent-runtime/.output ./.output
USER node
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
