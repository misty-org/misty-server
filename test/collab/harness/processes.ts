import { spawn, type ChildProcess } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const serverRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");

export interface Output {
  stdout: string;
  stderr: string;
}

export function baseEnv(extra: Record<string, string> = {}): Record<string, string> {
  const keep: Record<string, string> = {};
  for (const name of ["PATH", "HOME", "TMPDIR", "GOPATH", "GOCACHE", "GOMODCACHE", "GOFLAGS", "GOTOOLCHAIN", "DOCKER_HOST"]) {
    const value = process.env[name];
    if (value !== undefined) keep[name] = value;
  }
  return { ...keep, ...extra };
}

export function run(command: string, args: string[], options: { cwd?: string; env?: Record<string, string> } = {}): Promise<Output> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd: options.cwd ?? serverRoot, env: options.env ?? baseEnv(), stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => (stdout += chunk));
    child.stderr.on("data", (chunk) => (stderr += chunk));
    child.on("error", reject);
    child.on("close", (code) => {
      if (code === 0) resolve({ stdout, stderr });
      else reject(new Error(`${command} ${args.join(" ")} exited with ${code}\n${stdout}\n${stderr}`));
    });
  });
}

export interface Service {
  child: ChildProcess;
  log(): string;
  waitFor(pattern: RegExp, timeoutMs?: number, count?: number): Promise<RegExpMatchArray>;
  stop(signal?: NodeJS.Signals, timeoutMs?: number): Promise<number | null>;
  exited: Promise<number | null>;
}

export function start(command: string, args: string[], env: Record<string, string>): Service {
  const child = spawn(command, args, { cwd: serverRoot, env, stdio: ["ignore", "pipe", "pipe"] });
  let text = "";
  const waiters = new Set<() => void>();
  const append = (chunk: Buffer) => {
    text += chunk.toString();
    for (const waiter of [...waiters]) waiter();
  };
  child.stdout?.on("data", append);
  child.stderr?.on("data", append);
  const exited = new Promise<number | null>((resolve) => {
    child.on("exit", (code) => {
      for (const waiter of [...waiters]) waiter();
      resolve(code);
    });
  });
  let done = false;
  void exited.then(() => (done = true));
  return {
    child,
    exited,
    log: () => text,
    waitFor(pattern, timeoutMs = 30_000, count = 1) {
      return new Promise((resolve, reject) => {
        const global = new RegExp(pattern.source, pattern.flags.includes("g") ? pattern.flags : `${pattern.flags}g`);
        const check = () => {
          const found = [...text.matchAll(global)];
          if (found.length >= count) {
            finish();
            resolve(found[count - 1] as RegExpMatchArray);
            return;
          }
          if (done || child.exitCode !== null) {
            finish();
            reject(new Error(`${command} exited before printing ${pattern}\n${text}`));
          }
        };
        const timer = setTimeout(() => {
          finish();
          reject(new Error(`${command} didn't print ${pattern} within ${timeoutMs}ms\n${text}`));
        }, timeoutMs);
        const finish = () => {
          clearTimeout(timer);
          waiters.delete(check);
        };
        waiters.add(check);
        check();
      });
    },
    async stop(signal = "SIGTERM", timeoutMs = 30_000) {
      if (child.exitCode !== null || child.signalCode !== null) return child.exitCode;
      child.kill(signal);
      const timer = setTimeout(() => child.kill("SIGKILL"), timeoutMs);
      try {
        return await exited;
      } finally {
        clearTimeout(timer);
      }
    },
  };
}
