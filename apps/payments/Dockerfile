FROM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS build
WORKDIR /app
COPY package.json package-lock.json tsconfig.json tsconfig.build.json ./
COPY apps/api/package.json ./apps/api/package.json
COPY apps/payments/package.json ./apps/payments/package.json
COPY third-party ./third-party
RUN npm ci
COPY apps/api/src ./apps/api/src
COPY apps/payments/src ./apps/payments/src
COPY packages ./packages
RUN npm run build

FROM build AS production-dependencies
RUN rm -rf node_modules && npm ci --omit=dev --workspace @misty/payments --include-workspace-root

FROM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS runtime
WORKDIR /app
ENV NODE_ENV=production HOST=0.0.0.0 PORT=8083 BILLING_MIGRATIONS_DIR=/app/migrations
COPY --from=production-dependencies /app/node_modules ./node_modules
COPY package.json package-lock.json ./
COPY apps/payments/package.json ./apps/payments/package.json
COPY third-party ./third-party
COPY --from=build /app/dist/apps/payments ./dist/apps/payments
COPY --from=build /app/dist/packages ./dist/packages
COPY apps/payments/migrations ./migrations
USER node
EXPOSE 8083
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s CMD ["node", "--input-type=module", "-e", "const r=await fetch('http://127.0.0.1:8083/readyz',{signal:AbortSignal.timeout(2000)});process.exit(r.ok?0:1)"]
CMD ["node", "dist/apps/payments/src/main.js"]
