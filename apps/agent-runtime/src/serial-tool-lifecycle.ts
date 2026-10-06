/** A run owns one durable wait at a time. Hold its tool turn through the entire
 * approval/device continuation, including checkpoints, before starting another
 * tool proposed in the same model response. Workflow replay recreates this order.
 *
 * A stop ends the run. A pause only declines the rest of the current model
 * response so the model can plan again from what it saw. Declined calls never
 * execute: their tool reports the declined reason to the model instead. Start
 * never throws for a declined call, because the adapter would reject the whole
 * response instead of reporting that one call. */
export function serialToolLifecycle() {
  let tail: Promise<void> = Promise.resolve();
  let stoppedReason = "";
  let pausedReason = "";
  const releases = new Map<string, () => void>();
  const declined = new Map<string, string>();
  const release = (id: string) => {
    const resolve = releases.get(id);
    releases.delete(id);
    resolve?.();
  };
  const stop = (reason: string) => { stoppedReason ||= reason; };
  return {
    stop,
    pause(reason: string) { if (!stoppedReason) pausedReason ||= reason; },
    get stoppedReason() { return stoppedReason; },
    /** Clears a pause once its model response has drained, before the next turn. */
    resume() {
      if (!pausedReason || stoppedReason || releases.size !== 0) return false;
      pausedReason = "";
      declined.clear();
      return true;
    },
    /** The error a declined call reports instead of executing. */
    declined(id: string): string | undefined { return declined.get(id); },
    async start(id: string, checkpoint: () => Promise<void>) {
      if (releases.has(id)) throw new Error("duplicate_tool_call_id");
      const previous = tail;
      tail = new Promise<void>((resolve) => releases.set(id, resolve));
      try {
        await previous;
        if (stoppedReason || pausedReason) {
          declined.set(id, stoppedReason ? `tool_sequence_stopped: ${stoppedReason}` : `tool_not_attempted: ${pausedReason}`);
          return;
        }
        await checkpoint();
      } catch (error) {
        stop("The preceding action or its checkpoint did not complete.");
        release(id);
        throw error;
      }
    },
    async finish(id: string, checkpoint: () => Promise<void>) {
      try {
        await checkpoint();
      } catch (error) {
        stop("The preceding action checkpoint could not be confirmed.");
        throw error;
      } finally {
        release(id);
      }
    },
  };
}
