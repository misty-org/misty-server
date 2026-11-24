import net from "node:net";
import { inject } from "vitest";
import { databaseEnv } from "./postgres.js";
import { baseEnv, start, type Service } from "./processes.js";

export interface Instance {
  port: number;
  url: string;
  service: Service;
  log(): string;
  stop(signal?: NodeJS.Signals): Promise<number | null>;
}

const running = new Set<Instance>();

export async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close(() => resolve(port));
    });
  });
}

async function reachable(url: string): Promise<void> {
  let last: unknown;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${url}/health`);
      if (response.ok) return;
      last = new Error(`health answered ${response.status}`);
    } catch (error) {
      last = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw last;
}

export async function startCollab(options: { public?: string; port?: number } = {}): Promise<Instance> {
  const setup = inject("setup");
  const port = options.port ?? (await freePort());
  const url = `http://127.0.0.1:${port}`;
  const service = start(setup.collab, [], baseEnv({
    ...databaseEnv(setup.app),
    PORT: String(port),
    MISTY_AUTH_SIGNING_KEY: setup.signingKey,
    MISTY_COLLAB_SELF_URL: url,
    MISTY_COLLAB_URL: options.public ?? url,
  }));
  await service.waitFor(new RegExp(`Misty collab running on :${port}`));
  await reachable(url);
  const instance: Instance = {
    port,
    url,
    service,
    log: () => service.log(),
    async stop(signal = "SIGTERM") {
      running.delete(instance);
      return service.stop(signal);
    },
  };
  running.add(instance);
  return instance;
}

export async function stopAll(): Promise<void> {
  await Promise.all([...running].map((instance) => instance.stop()));
}

export interface Edge {
  url: string;
  port: number;
  to(instance: Instance): void;
  close(): Promise<void>;
}

export async function startEdge(): Promise<Edge> {
  let target: Instance | null = null;
  const sockets = new Set<net.Socket>();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    if (!target) {
      socket.destroy();
      return;
    }
    const upstream = net.connect(target.port, "127.0.0.1");
    sockets.add(upstream);
    const end = () => {
      socket.destroy();
      upstream.destroy();
      sockets.delete(socket);
      sockets.delete(upstream);
    };
    socket.on("error", end);
    upstream.on("error", end);
    socket.on("close", end);
    upstream.on("close", end);
    socket.pipe(upstream);
    upstream.pipe(socket);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  const port = typeof address === "object" && address ? address.port : 0;
  return {
    url: `http://127.0.0.1:${port}`,
    port,
    to(instance) {
      target = instance;
    },
    close: () =>
      new Promise<void>((resolve) => {
        for (const socket of sockets) socket.destroy();
        server.close(() => resolve());
      }),
  };
}
