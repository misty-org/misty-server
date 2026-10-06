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

  const requireInspection = (name: string) => {
    requiredInspection ||= name;
    order.pause("A fresh observation is required before another action.");
  };

  const settle = (name: string, call: ToolCall, outcome: ToolOutcome, visible: string) => {
    if (outcome.kind === "confirmed") {
      if (name === requiredInspection) requiredInspection = "";
      // Native workspace input consumes its screenshot even on success.
      if (name === "browser.workspace.interact" && catalog.modelName("browser.workspace.visual")) requireInspection("browser.workspace.visual");
      return;
    }
    if (outcome.kind === "handoff") {
      // Nothing failed: the conversation continues once the screen is attached.
      handoff ||= outcome.reason;
      order.stop(outcome.reason);
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
      const name = catalog.mistyName(toolCall.toolName);
      await checkpoint({
        node_id: `tool:${toolCall.toolCallId}`, state: "running", phase: `using_${name.replaceAll(".", "_")}`, progress: 40,
        input: typeof toolCall.input === "object" && toolCall.input !== null ? toolCall.input as Record<string, unknown> : {},
      });
    }),
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
