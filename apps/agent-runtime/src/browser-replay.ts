import type { HarnessCheckpoint } from "./harness.js";
import { classifyToolOutcome, handoffMessage } from "./tool-outcomes.js";

/**
 * Replays a workflow's saved browser actions before any model call
 * (docs/agents/harness.md, Workflows as memory). Each saved step names its control by
 * locator; the device finds it on the current page. Calls run through the same
 * gateway as the model's: authorized, journaled and approval-gated. The first
 * step the page no longer matches hands the task to the agent.
 */
export interface BrowserReplay {
  scope_id: string;
  start_url?: string;
  calls: Array<{ tool: string; args: Record<string, unknown> }>;
  recorded_at?: string;
}

type ReplayOutcome =
  | { kind: "completed"; text: string; actions: number }
  /** The page no longer matched; the agent continues from here. */
  | { kind: "diverged"; note: string; actions: number }
  /** The person must act (an approval card); the run hands off. */
  | { kind: "handoff"; text: string }
  /** An action's outcome is unknown; the run must stop, never retry. */
  | { kind: "stopped"; text: string };

interface ReplayDeps {
  run(callId: string, name: string, input: Record<string, unknown>): Promise<unknown>;
  checkpoint(event: HarnessCheckpoint): Promise<void>;
  available(name: string): boolean;
}

const replayTools = new Set(["browser.navigate", "browser.steps", "browser.skill", "browser.wait_for"]);
const MAX_CALLS = 100;

export function validReplay(value: unknown): value is BrowserReplay {
  if (!value || typeof value !== "object") return false;
  const replay = value as BrowserReplay;
  return typeof replay.scope_id === "string" && replay.scope_id.length >= 8 && Array.isArray(replay.calls) &&
    replay.calls.length > 0 && replay.calls.length <= MAX_CALLS &&
    replay.calls.every((call) => !!call && replayTools.has(call.tool) && !!call.args && typeof call.args === "object");
}

/** Why a call's result means its step did not happen as recorded, or "". */
function mismatch(tool: string, args: Record<string, unknown>, output: unknown): string {
  const result = (output ?? {}) as Record<string, any>;
  if (result.status === "failure") return String(result.tool_error?.message ?? result.reason ?? "The step was refused.");
  switch (tool) {
    case "browser.steps": {
      const wanted = Array.isArray(args.steps) ? args.steps.length : 0;
      if (result.completed === wanted) return "";
      const failed = (result.steps as Array<Record<string, unknown>> | undefined)?.find((step) => step.ok !== true);
      return String(failed?.error ?? result.stopped ?? "Not every step ran.");
    }
    case "browser.skill":
      return result.ok === true ? "" : "The skill did not finish.";
    case "browser.wait_for":
      return result.met === true ? "" : `“${String(args.text ?? args.textGone ?? args.urlIncludes ?? "")}” did not show up.`;
    default:
      return "";
  }
}

export async function runBrowserReplay(replay: BrowserReplay, deps: ReplayDeps): Promise<ReplayOutcome> {
  const scopeId = replay.scope_id;
  const calls = [...replay.calls];
  if (replay.start_url && calls[0]?.tool !== "browser.navigate") calls.unshift({ tool: "browser.navigate", args: { url: replay.start_url } });
  if (!calls.every((call) => deps.available(call.tool)) || !deps.available("browser.snapshot")) {
    return { kind: "diverged", note: "", actions: 0 };
  }
  let actions = 0;
  const call = async (id: string, name: string, input: Record<string, unknown>) => {
    await deps.checkpoint({ node_id: `tool:${id}`, state: "running", phase: `using_${name.replaceAll(".", "_")}`, progress: 30, input });
    try {
      const output = await deps.run(id, name, input);
      await deps.checkpoint({ node_id: `tool:${id}`, state: "completed", phase: "working", progress: 50, output: { tool: name, success: true, replay: true } });
      return { output } as const;
    } catch (error) {
      await deps.checkpoint({ node_id: `tool:${id}`, state: "failed", phase: "tool_failed", progress: 40, output: { tool: name, success: false, replay: true } }).catch(() => undefined);
      return { error } as const;
    }
  };
  for (const [index, recorded] of calls.entries()) {
    const id = `replay-${index + 1}`;
    let input: Record<string, unknown> = { ...recorded.args, scopeId };
    if (recorded.tool === "browser.steps" || recorded.tool === "browser.skill") {
      // Steps act on the page as it is now: read it first for its documentId.
      const snapshot = await call(`${id}-snapshot`, "browser.snapshot", { scopeId, maxChars: 2000 });
      const documentId = (snapshot.output as Record<string, unknown> | undefined)?.documentId;
      if (typeof documentId !== "string") return diverged(index, calls.length, actions, "The page could not be read.");
      input = { ...input, documentId, includeSnapshot: false };
    }
    const result = await call(id, recorded.tool, input);
    const outcome = classifyToolOutcome({
      success: !("error" in result), output: "output" in result ? result.output : undefined,
      error: "error" in result ? result.error : undefined, readOnly: recorded.tool === "browser.wait_for", rejected: false,
    });
    if (outcome.kind === "handoff") return { kind: "handoff", text: handoffMessage("output" in result ? result.output : undefined) || outcome.reason };
    if (outcome.kind === "stop") return { kind: "stopped", text: outcome.reason };
    const reason = outcome.kind === "retry" ? outcome.reason : mismatch(recorded.tool, input, "output" in result ? result.output : undefined);
    if (reason) return diverged(index, calls.length, actions, reason);
    actions++;
  }
  return { kind: "completed", text: `Ran this workflow's saved browser steps (${actions} actions); every one matched the page as recorded.`, actions };
}

function diverged(index: number, total: number, actions: number, reason: string): ReplayOutcome {
  return {
    kind: "diverged", actions,
    note: `[Saved replay] Misty replayed ${index} of ${total} saved browser actions of this workflow, then stopped at action ${index + 1}: ${reason.slice(0, 500)} The browser is in that state now. Take a snapshot and continue the task from here; do not redo actions that already ran.`,
  };
}
