import { expect, it } from "vitest";
import { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";

it("declines the rest of a paused response and resumes for the next model turn", async () => {
  const order = serialToolLifecycle();
  await order.start("stale-click", async () => {});
  let dependentStarted = false;
  const queued = order.start("dependent-write", async () => { dependentStarted = true; });
  order.pause("A fresh observation is required before another action.");
  expect(order.resume()).toBe(false);
  await order.finish("stale-click", async () => {});
  await queued;
  expect(dependentStarted).toBe(false);
  expect(order.declined("dependent-write")).toContain("tool_not_attempted");
  await order.finish("dependent-write", async () => {});
  expect(order.resume()).toBe(true);
  await order.start("fresh-inspection", async () => {});
  expect(order.declined("fresh-inspection")).toBeUndefined();
  await order.finish("fresh-inspection", async () => {});
});

it("never recovers when the rejection checkpoint itself is uncertain", async () => {
  const order = serialToolLifecycle();
  await order.start("stale-click", async () => {});
  order.pause("A fresh observation is required before another action.");
  await expect(order.finish("stale-click", async () => { throw new Error("checkpoint failed"); })).rejects.toThrow();
  expect(order.resume()).toBe(false);
  await order.start("next", async () => {});
  expect(order.declined("next")).toContain("tool_sequence_stopped");
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

it("declines queued calls with a stop after an uncertain effect or checkpoint failure", async () => {
  for (const checkpointFails of [false, true]) {
    const order = serialToolLifecycle();
    await order.start("send", async () => {});
    let dependentStarted = false;
    const later = order.start("follow-up", async () => { dependentStarted = true; });
    if (checkpointFails) {
      await expect(order.finish("send", async () => { throw new Error("lost checkpoint response"); })).rejects.toThrow("lost checkpoint");
    } else {
      order.stop("Delivery is uncertain");
      await order.finish("send", async () => {});
    }
    await later;
    expect(dependentStarted).toBe(false);
    expect(order.declined("follow-up")).toContain("tool_sequence_stopped");
    await order.finish("follow-up", async () => {});
    expect(order.resume()).toBe(false);
    await order.start("next-model-turn", async () => {});
    expect(order.declined("next-model-turn")).toContain("tool_sequence_stopped");
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

it("holds queued typing after a dispatched click until a fresh observation turn", async () => {
  const order = serialToolLifecycle();
  const dispatched: string[] = [];
  await order.start("click", async () => { dispatched.push("click"); });
  const oldTyping = order.start("type-old-frame", async () => { dispatched.push("wrong typing"); });
  await order.finish("click", async () => { order.pause("A fresh screenshot is required before another action."); });
  await oldTyping;
  expect(order.declined("type-old-frame")).toContain("tool_not_attempted");
  await order.finish("type-old-frame", async () => {});
  expect(dispatched).toEqual(["click"]);
  expect(order.resume()).toBe(true);
  await order.start("fresh-frame", async () => { dispatched.push("capture"); });
  await order.finish("fresh-frame", async () => {});
  await order.start("type-fresh-frame", async () => { dispatched.push("type"); });
  await order.finish("type-fresh-frame", async () => {});
  expect(dispatched).toEqual(["click", "capture", "type"]);
});
