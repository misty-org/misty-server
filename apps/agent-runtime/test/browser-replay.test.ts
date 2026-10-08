import { expect, it } from "vitest";
import { runBrowserReplay, validReplay, type BrowserReplay } from "../src/browser-replay.js";

const replay: BrowserReplay = {
  scope_id: "scope-12345678",
  start_url: "https://app.example/form",
  calls: [
    { tool: "browser.steps", args: { steps: [{ action: "fill", locator: { role: "textbox", name: "Name" }, text: "Ada" }] } },
    { tool: "browser.wait_for", args: { text: "Saved" } },
  ],
};

function deps(results: Record<string, unknown | Error>) {
  const calls: Array<{ id: string; name: string; input: Record<string, unknown> }> = [];
  return {
    calls,
    deps: {
      run: async (id: string, name: string, input: Record<string, unknown>) => {
        calls.push({ id, name, input });
        const result = results[name];
        if (result instanceof Error) throw result;
        return result;
      },
      checkpoint: async () => {},
      available: () => true,
    },
  };
}

it("replays every saved step without a model and opens the start page first", async () => {
  const { calls, deps: d } = deps({
    "browser.navigate": { ok: true }, "browser.snapshot": { documentId: "doc-1" },
    "browser.steps": { completed: 1, steps: [{ ok: true }] }, "browser.wait_for": { met: true },
  });
  const outcome = await runBrowserReplay(replay, d);
  expect(outcome).toMatchObject({ kind: "completed", actions: 3 });
  expect(calls.map((call) => call.name)).toEqual(["browser.navigate", "browser.snapshot", "browser.steps", "browser.wait_for"]);
  // Steps act on the current page's document, in the run's own browser.
  expect(calls[2]!.input).toMatchObject({ scopeId: "scope-12345678", documentId: "doc-1", includeSnapshot: false });
  expect(new Set(calls.map((call) => call.id)).size).toBe(calls.length);
});

it("hands the task to the agent at the first step the page no longer matches", async () => {
  const { deps: d } = deps({
    "browser.navigate": { ok: true }, "browser.snapshot": { documentId: "doc-1" },
    "browser.steps": { status: "failure", attempted: false, tool_error: { code: "browser_locator_missing", message: "No textbox named “Name” on this page." } },
  });
  const outcome = await runBrowserReplay(replay, d);
  expect(outcome.kind).toBe("diverged");
  expect(outcome.kind === "diverged" && outcome.note).toContain("stopped at action 2");
  expect(outcome.kind === "diverged" && outcome.note).toContain("No textbox named");
});

it("stops the run when an action's outcome is unknown, and hands off for approval", async () => {
  const unknown = await runBrowserReplay(replay, deps({ "browser.navigate": new Error("tool_sequence_stopped: delivery uncertain") }).deps);
  expect(unknown.kind).toBe("stopped");
  const approval = await runBrowserReplay(replay, deps({
    "browser.navigate": { ok: true }, "browser.snapshot": { documentId: "doc-1" },
    "browser.steps": { status: "awaiting_approval", user_message: "“Act on a web page” is waiting for your approval above." },
  }).deps);
  expect(approval).toEqual({ kind: "handoff", text: "“Act on a web page” is waiting for your approval above." });
});

it("accepts only well-formed replays of browser actions", () => {
  expect(validReplay(replay)).toBe(true);
  expect(validReplay({ ...replay, calls: [] })).toBe(false);
  expect(validReplay({ ...replay, calls: [{ tool: "notes.create", args: {} }] })).toBe(false);
  expect(validReplay({ ...replay, scope_id: "x" })).toBe(false);
  expect(validReplay(null)).toBe(false);
});
