import { browserReinspectionTool, requiresBrowserReinspection } from "./browser-reinspection.js";
import type { HarnessCheckpoint } from "./harness.js";
import type { ModelCatalog } from "./model-tools.js";
import { visibleErrorMessage } from "./runtime-errors.js";
import type { serialToolLifecycle } from "./serial-tool-lifecycle.js";
import { classifyToolOutcome, toolFailureSignature, unconfirmedToolResultReason, type ToolOutcome } from "./tool-outcomes.js";

interface ToolCall { toolCallId: string; toolName: string; input: unknown }
interface ToolEnd { toolCall: ToolCall; durationMs: number; success: boolean; output?: unknown; error?: unknown }

interface ToolMonitorOptions {
  catalog: ModelCatalog;
  order: ReturnType<typeof serialToolLifecycle>;
  checkpoint(event: HarnessCheckpoint): Promise<void>;
  /** Calls Misty rejected without any effect. */
  rejected: ReadonlySet<string>;
}

/**
 * Follows every tool call across model turns. A confirmed call continues the
 * response. A call that had no effect pauses the rest of the response so the
 * model plans again from its error. A write repeated after the same rejection,
 * or an outcome the user must resolve, stops the run.
 */
export function toolMonitor({ catalog, order, checkpoint, rejected }: ToolMonitorOptions) {
  // Misty name of the observation the next turn must make before acting.
  let requiredInspection = "";
  let handoff = "";
  const failures: Array<{ toolName: string; error: string }> = [];
  const rejectedWrites = new Map<string, number>();
  let unopenedTabs = 0;
  let unclosedTabs = 0;
  let unanswered = 0;

  const requireInspection = (name: string) => {
    requiredInspection ||= name;
    order.pause("A fresh observation is required before another action.");
  };

  const settle = (name: string, call: ToolCall, outcome: ToolOutcome, visible: string) => {
    if (outcome.kind === "confirmed") {
      if (name === requiredInspection) requiredInspection = "";
      return;
    }
    if (outcome.kind === "handoff") {
      // Nothing failed: the conversation continues once the screen is attached.
      handoff ||= outcome.reason;
      order.stop(outcome.reason);
      return;
    }
    // An ordinary page action whose effect couldn't be confirmed (a toggle, a menu) is
    // looked at again rather than ending the run. A consequential one still stops:
    // it may have sent, paid or deleted, and must never be planned a second time.
    if (outcome.kind === "stop" && uncertainPageAction(name, call.input)) {
      const look = catalog.modelName("browser.snapshot") ? "browser.snapshot" : "browser.inspect";
      requireInspection(look);
      return;
    }
    // Opening a tab of the agent's own changes nothing for anyone, so one that couldn't be
    // confirmed (the device was slow to answer) is planned around instead of ending the run.
    if (outcome.kind === "stop" && name === "screen.open" && ownTab(call.input) && ++unopenedTabs <= 2) {
      order.pause(`Opening that tab did not finish (${visible}). Navigate a tab you already have, open it again, or read the page with web_fetch.`);
      return;
    }
    // Closing a tab of the agent's own is just as harmless: one that failed (a login refresh at that
    // moment failed four closes in one response and turned a finished run into "didn't finish")
    // leaves the tab open, which costs nothing, so the run carries on.
    if (outcome.kind === "stop" && name === "screen.close" && ++unclosedTabs <= 8) {
      order.pause(`Closing that tab did not finish (${visible}). It stays open; carry on with the task, and close it later only if it still matters.`);
      return;
    }
    // Asking the person for help (a bot check, a sign-in) changes no page either: when nobody
    // can answer, the run takes another route instead of ending with nothing.
    if (outcome.kind === "stop" && name === "browser.request_user_action" && ++unanswered <= 2) {
      order.pause(`Nobody could take over that page (${visible}). Reach what you need another way: the same page from a search result, another site that has it, or web_fetch; report it as blocked only if nothing else has it.`);
      return;
    }
    if (outcome.kind === "retry") {
      const signature = toolFailureSignature(name, call.input);
      const repeats = catalog.readOnly(call.toolName) ? 0 : (rejectedWrites.get(signature) ?? 0) + 1;
      rejectedWrites.set(signature, repeats);
      if (repeats < 2) {
        order.pause(`An earlier call in this response failed: ${visible}`);
        return;
      }
    }
    failures.push({ toolName: name, error: visible });
    order.stop(visible);
  };

  return {
    get requiredInspection() { return requiredInspection; },
    /** Set when the run ended to open a screen. */
    get handoff() { return handoff; },
    failures: () => failures,
    /** Every tool, or only the observation a stale or consumed screen needs. */
    activeTools(all: string[]): string[] {
      const inspection = requiredInspection && catalog.modelName(requiredInspection);
      return inspection ? [inspection] : all;
    },
    onStart: ({ toolCall }: { toolCall: ToolCall }) => order.start(toolCall.toolCallId, async () => {
      // Misty announces a read-only call itself when the call arrives, so a
      // read costs one checkpoint, on finish (agent_runtime_tool_start.go).
      if (catalog.readOnly(toolCall.toolName)) return;
      const name = catalog.mistyName(toolCall.toolName);
      await checkpoint({
        node_id: `tool:${toolCall.toolCallId}`, state: "running", phase: `using_${name.replaceAll(".", "_")}`, progress: 40,
        input: typeof toolCall.input === "object" && toolCall.input !== null ? toolCall.input as Record<string, unknown> : {},
      });
    }, catalog.concurrent(toolCall.toolName)),
    onEnd: (event: ToolEnd) => order.finish(event.toolCall.toolCallId, async () => {
      const { toolCall } = event;
      if (order.declined(toolCall.toolCallId)) return;
      const name = catalog.mistyName(toolCall.toolName);
      const observation = browserReinspectionTool(name);
      // A stale reference was rejected before dispatch: observe, then replan.
      const stale = event.success && catalog.modelName(observation) !== undefined && requiresBrowserReinspection(name, event.output);
      const outcome = classifyToolOutcome({
        success: event.success, output: event.output, error: event.error,
        readOnly: catalog.readOnly(toolCall.toolName), rejected: rejected.has(toolCall.toolCallId),
      });
      const confirmed = outcome.kind === "confirmed" || outcome.kind === "handoff";
      const visible = confirmed ? "" : (event.success ? unconfirmedToolResultReason(event.output) : "") || visibleErrorMessage(event.error);
      if (stale) requireInspection(observation);
      else settle(name, toolCall, outcome, visible);
      await checkpoint({
        node_id: `tool:${toolCall.toolCallId}`, state: confirmed ? "completed" : "failed", phase: confirmed ? "working" : "tool_failed",
        progress: confirmed ? 60 : 40, output: { tool: name, duration_ms: event.durationMs, success: confirmed },
        error_message: confirmed ? undefined : visible,
      });
    }),
  };
}

/** screen_open for a tab of the agent's own, not the person's tab or desktop. */
function ownTab(input: unknown): boolean {
  const target = (input && typeof input === "object" ? (input as Record<string, unknown>).target : undefined) ?? "";
  return target === "" || target === "new_tab" || target === "window";
}

/** A browser page action, not marked consequential, whose outcome was uncertain. */
function uncertainPageAction(name: string, input: unknown): boolean {
  if (!name.startsWith("browser.") || name.startsWith("browser.workspace.")) return false;
  const fields = (input && typeof input === "object" ? input : {}) as Record<string, unknown>;
  if (fields.allowConsequential === true) return false;
  const steps = Array.isArray(fields.steps) ? fields.steps : [];
  return !steps.some((step) => step && typeof step === "object" && (step as Record<string, unknown>).allowConsequential === true);
}
