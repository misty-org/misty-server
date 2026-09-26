/** A run owns one durable wait at a time. Hold its tool turn through the entire
 * approval/device continuation, including checkpoints, before starting another
 * tool proposed in the same model response. Workflow replay recreates this order. */
export function serialToolLifecycle() {
  let tail: Promise<void> = Promise.resolve();
  let stoppedReason = "";
  let reinspectionRequested = false;
  const stop = (reason: string) => { stoppedReason ||= reason; reinspectionRequested = false; };
  const releases = new Map<string, () => void>();
  const release = (id: string) => {
    const resolve = releases.get(id);
    releases.delete(id);
    resolve?.();
  };
  return {
    stop,
    requestReinspection() {
      if (!stoppedReason) {
        stoppedReason = "The browser action was not attempted. Inspect the page again.";
        reinspectionRequested = true;
      }
    },
    get reinspectionRequested() { return reinspectionRequested; },
    resumeForReinspection() {
      if (!reinspectionRequested || releases.size !== 0) return false;
      stoppedReason = "";
      reinspectionRequested = false;
      return true;
    },
    get stoppedReason() { return stoppedReason; },
    async start(id: string, checkpoint: () => Promise<void>) {
      if (releases.has(id)) throw new Error("duplicate_tool_call_id");
      const previous = tail;
      tail = new Promise<void>((resolve) => releases.set(id, resolve));
      try {
        await previous;
        if (stoppedReason) throw new Error(reinspectionRequested ? "browser_reinspection_required" : `tool_sequence_stopped: ${stoppedReason}`);
        await checkpoint();
      } catch (error) {
        if (!(reinspectionRequested && error instanceof Error && error.message === "browser_reinspection_required"))
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
