FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS builder
WORKDIR /src
COPY package.json package-lock.json ./
COPY agent-runtime/package.json agent-runtime/package.json
COPY app/package.json app/package.json
COPY extensions/package.json extensions/package.json
COPY website/package.json website/package.json
RUN npm ci --workspace=@misty/agent-runtime
COPY agent-runtime agent-runtime
RUN npm run build --workspace=@misty/agent-runtime

FROM builder AS setup
CMD ["npm", "run", "world:setup", "--workspace=@misty/agent-runtime"]

FROM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS runtime
WORKDIR /app
ENV NODE_ENV=production PORT=3000
COPY --from=builder /src/node_modules ./node_modules
COPY --from=builder /src/agent-runtime/.output ./.output
USER node
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
