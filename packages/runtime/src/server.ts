import { getRequestListener } from "@hono/node-server";
import { createServer } from "node:http";
import type { Hono } from "hono";
import type { RuntimeConfig } from "./config.js";
import type { HttpEnvironment } from "./http.js";
import type { Logger } from "./logger.js";

export function startHttpServer(options: {
  app: Hono<HttpEnvironment>;
  config: RuntimeConfig;
  logger: Logger;
  drain: () => Promise<void>;
  markDraining: () => void;
}) {
  const { app, config, logger } = options;
  const server = createServer({ maxHeaderSize: 65536 }, getRequestListener(app.fetch));
  server.headersTimeout = 10000;
  server.requestTimeout = 60000;
  server.keepAliveTimeout = 120000;
  server.maxHeadersCount = 100;
  server.listen(config.port, config.host, () => {
    logger.info({ host: config.host, port: config.port }, "server listening");
  });
  let closing: Promise<void> | undefined;

  const close = (): Promise<void> => {
    if (closing) return closing;
    options.markDraining();
    closing = (async () => {
      const timeout = setTimeout(() => server.closeAllConnections(), 20000);
      timeout.unref();
      try {
        await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
        await options.drain();
        logger.info("server stopped");
      } finally {
        clearTimeout(timeout);
      }
    })();
    return closing;
  };
  return { server, close };
}
