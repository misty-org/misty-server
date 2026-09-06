import bcrypt from "bcrypt";
import { randomBytes } from "node:crypto";
import { AuthBusy } from "./model.js";

/** Native asynchronous bcrypt, bounded before it reaches the libuv work queue. */
export async function createPasswordHasher(options: { concurrency?: number; maxQueued?: number } = {}) {
  const concurrency = options.concurrency ?? 4, maxQueued = options.maxQueued ?? 32;
  if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 32 || !Number.isInteger(maxQueued) || maxQueued < 0 || maxQueued > 1000) throw new Error("Invalid password work limits");
  const dummy = await bcrypt.hash(randomBytes(32).toString("base64url"), 10);
  let active = 0;
  const waiters: Array<() => void> = [];
  async function run<T>(operation: () => Promise<T>): Promise<T> {
    if (active >= concurrency) {
      if (waiters.length >= maxQueued) throw new AuthBusy("Authentication work queue is full");
      await new Promise<void>((resolve) => waiters.push(resolve));
    } else active++;
    try { return await operation(); }
    finally { const next = waiters.shift(); if (next) next(); else active--; }
  }
  return {
    async hash(password: string) {
      if (Buffer.byteLength(password, "utf8") > 72) throw new Error("Password exceeds bcrypt's byte limit");
      return run(() => bcrypt.hash(password, 10));
    },
    async verify(password: string, hash: string | null) {
      // Unknown users still perform the same default-cost password work.
      const supported = hash !== null && /^\$2[ab]\$\d{2}\$[./A-Za-z0-9]{53}$/.test(hash);
      const valid = await run(() => bcrypt.compare(password, supported ? hash : dummy));
      return supported && valid;
    },
  };
}
export type PasswordHasher = Awaited<ReturnType<typeof createPasswordHasher>>;
