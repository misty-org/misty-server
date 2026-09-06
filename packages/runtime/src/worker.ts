import { setTimeout } from "node:timers/promises";
import type { Logger } from "./logger.js";

/** One bounded operation at a time; durable leases allow multiple processes. */
export function startPollingWorker(options: {
  name: string;
  runOnce: () => Promise<boolean>;
  logger: Logger;
  idleMilliseconds?: number;
}) {
  const stop = new AbortController();
  const completion = (async () => {
    while (!stop.signal.aborted) {
      let worked = false;
      try { worked = await options.runOnce(); }
      catch (error) {
        options.logger.error({ worker: options.name, errorType: error instanceof Error ? error.name : "Unknown" }, "worker operation failed");
      }
      if (!worked && !stop.signal.aborted) {
        try { await setTimeout(options.idleMilliseconds ?? 1000, undefined, { signal: stop.signal }); }
        catch { if (!stop.signal.aborted) throw new Error("Worker delay failed"); }
      }
    }
  })();
  return { stop: async () => { stop.abort(); await completion; } };
}
