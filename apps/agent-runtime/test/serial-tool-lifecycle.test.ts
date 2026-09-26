import { expect, it } from "vitest";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";

it("blocks the current batch after a pre-dispatch rejection and can start a fresh inspection turn", async () => {
  const order = serialToolLifecycle();
  await order.start("stale-click", async () => {});
  const queued = expect(order.start("dependent-write", async () => { throw new Error("must not execute"); })).rejects.toThrow("browser_reinspection_required");
  order.requestReinspection();
  expect(order.resumeForReinspection()).toBe(false);
  await order.finish("stale-click", async () => {});
  await queued;
  expect(order.resumeForReinspection()).toBe(true);
  await order.start("fresh-inspection", async () => {});
  await order.finish("fresh-inspection", async () => {});
});

it("never recovers when the rejection checkpoint itself is uncertain", async () => {
  const order = serialToolLifecycle();
  await order.start("stale-click", async () => {});
  order.requestReinspection();
  await expect(order.finish("stale-click", async () => { throw new Error("checkpoint failed"); })).rejects.toThrow();
  expect(order.resumeForReinspection()).toBe(false);
  await expect(order.start("next", async () => {})).rejects.toThrow("tool_sequence_stopped");
});

it("holds later tool starts behind a durable approval/device wait and its end checkpoint", async () => {
  const order = serialToolLifecycle();
  const events: string[] = [];
  await order.start("send", async () => { events.push("send:start"); });
  const later = order.start("note", async () => { events.push("note:start"); });
  // Resuming a hook does not call finish; the resumed effect and its checkpoint
  // must settle first. A second tool cannot overwrite the first run's wait.
  await Promise.resolve();
  expect(events).toEqual(["send:start"]);
  await order.finish("send", async () => { events.push("send:end"); });
  await later;
  expect(events).toEqual(["send:start", "send:end", "note:start"]);
  await order.finish("note", async () => { events.push("note:end"); });
});

it("releases queued calls with a stop after an uncertain effect or checkpoint failure", async () => {
  for (const checkpointFails of [false, true]) {
    const order = serialToolLifecycle();
    await order.start("send", async () => {});
    let dependentStarted = false;
    const later = expect(order.start("follow-up", async () => { dependentStarted = true; })).rejects.toThrow("tool_sequence_stopped");
    if (checkpointFails) {
      await expect(order.finish("send", async () => { throw new Error("lost checkpoint response"); })).rejects.toThrow("lost checkpoint");
    } else {
      order.stop("Delivery is uncertain");
      await order.finish("send", async () => {});
    }
    await later;
    expect(dependentStarted).toBe(false);
    await expect(order.start("next-model-turn", async () => {})).rejects.toThrow("tool_sequence_stopped");
  }
});

it("rejects duplicate in-flight identities without releasing the original call", async () => {
  const order = serialToolLifecycle();
  await order.start("send", async () => {});
  await expect(order.start("send", async () => {})).rejects.toThrow("duplicate_tool_call_id");
  let started = false;
  const later = order.start("next", async () => { started = true; });
  await Promise.resolve();
  expect(started).toBe(false);
  await order.finish("send", async () => {});
  await later;
  expect(started).toBe(true);
  await order.finish("next", async () => {});
});
