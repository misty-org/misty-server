import { createHash } from "node:crypto";
import * as Y from "yjs";

export type Source = (fire: () => void) => () => void;

export class Recorder<T> {
  readonly items: T[] = [];
  private readonly listeners = new Set<() => void>();

  push(item: T) {
    this.items.push(item);
    for (const listener of [...this.listeners]) listener();
  }

  last(): T | undefined {
    return this.items.at(-1);
  }

  some(predicate: (item: T) => boolean): boolean {
    return this.items.some(predicate);
  }

  readonly source: Source = (fire) => {
    this.listeners.add(fire);
    return () => this.listeners.delete(fire);
  };
}

export const onDoc =
  (doc: Y.Doc): Source =>
  (fire) => {
    doc.on("update", fire);
    return () => doc.off("update", fire);
  };

export const onAwareness =
  (awareness: { on(name: "change", fn: () => void): void; off(name: "change", fn: () => void): void }): Source =>
  (fire) => {
    awareness.on("change", fire);
    return () => awareness.off("change", fire);
  };

export function until(check: () => boolean, sources: Source[], timeoutMs = 10_000, what = "the condition"): Promise<void> {
  return new Promise((resolve, reject) => {
    const stops: (() => void)[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = () => {
      clearTimeout(timer);
      for (const stop of stops.splice(0)) stop();
    };
    const test = () => {
      let held = false;
      try {
        held = check();
      } catch (error) {
        finish();
        reject(error);
        return;
      }
      if (held) {
        finish();
        resolve();
      }
    };
    timer = setTimeout(() => {
      finish();
      reject(new Error(`timed out after ${timeoutMs}ms waiting for ${what}`));
    }, timeoutMs);
    for (const source of sources) stops.push(source(() => queueMicrotask(test)));
    test();
  });
}

export const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

export const fingerprint = (doc: Y.Doc) =>
  createHash("sha256").update(Y.encodeSnapshot(Y.snapshot(doc))).digest("base64");

export function untilAsync(check: () => Promise<boolean>, sources: Source[], timeoutMs = 10_000, what = "the condition"): Promise<void> {
  return new Promise((resolve, reject) => {
    const stops: (() => void)[] = [];
    let settled = false;
    let running = false;
    let again = false;
    const finish = () => {
      settled = true;
      clearTimeout(timer);
      for (const stop of stops.splice(0)) stop();
    };
    const test = async () => {
      if (settled) return;
      if (running) {
        again = true;
        return;
      }
      running = true;
      try {
        do {
          again = false;
          if (await check()) {
            finish();
            resolve();
            return;
          }
        } while (again && !settled);
      } catch (error) {
        finish();
        reject(error);
      } finally {
        running = false;
      }
    };
    const timer = setTimeout(() => {
      finish();
      reject(new Error(`timed out after ${timeoutMs}ms waiting for ${what}`));
    }, timeoutMs);
    for (const source of sources) stops.push(source(() => void test()));
    void test();
  });
}
