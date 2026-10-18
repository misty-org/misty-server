import type { ModelMessage } from "ai";
import { pruneStaleCodeResults } from "./code-context.js";
import { applyCompaction, CLEAR_AT, clearOldToolResults, COMPACT_AT, estimateInputTokens, messageBytes, planCompaction } from "./context-compaction.js";
import { pruneStalePageSnapshots } from "./prompt-cache.js";

// One condenser interface (docs/plans/agent-harness.md, phase 6): every way the
// runtime keeps a transcript inside the model's window is a step with the same
// shape, run in order from the cheapest and most cache-friendly to the most
// lossy. Adding a step means adding it here, not in the agent loop.

export interface ContextPressure {
  /** Estimated input tokens for the next call. */
  estimate: number;
  /** The model's context window in tokens. */
  window: number;
}

export interface Condenser {
  name: string;
  /** The new transcript and the estimate after it, or undefined to leave it as it was. */
  condense(messages: ModelMessage[], pressure: ContextPressure): Promise<{ messages: ModelMessage[]; estimate: number } | undefined>;
}

/** Scale an estimate by how much a step shrank the transcript. */
const shrink = (before: ModelMessage[], after: ModelMessage[], estimate: number) => estimate * (messageBytes(after) / Math.max(1, messageBytes(before)));

/** Stale page snapshots, in batches that keep most turns cached. */
export const staleSnapshots: Condenser = {
  name: "stale_snapshots",
  async condense(messages, { estimate }) {
    const pruned = pruneStalePageSnapshots(messages);
    return pruned.dropped ? { messages: pruned.messages, estimate: shrink(messages, pruned.messages, estimate) } : undefined;
  },
};

/** Superseded file reads and old command output in code projects. */
export const staleCodeResults: Condenser = {
  name: "stale_code_results",
  async condense(messages, { estimate }) {
    const pruned = pruneStaleCodeResults(messages);
    return pruned.dropped ? { messages: pruned.messages, estimate: shrink(messages, pruned.messages, estimate) } : undefined;
  },
};

/** Old tool results, once the transcript nears the window. */
export const oldToolResults: Condenser = {
  name: "old_tool_results",
  async condense(messages, { estimate, window }) {
    if (estimate <= window * CLEAR_AT) return undefined;
    const cleared = clearOldToolResults(messages);
    return cleared.cleared ? { messages: cleared.messages, estimate: shrink(messages, cleared.messages, estimate) } : undefined;
  },
};

/** A model-written summary of older steps, the last resort. A failed summary leaves the transcript as it was. */
export function summarizedSteps(summarize: (older: ModelMessage[]) => Promise<string>): Condenser {
  return {
    name: "summarized_steps",
    async condense(messages, { estimate, window }) {
      if (estimate <= window * COMPACT_AT) return undefined;
      const plan = planCompaction(messages);
      if (!plan) return undefined;
      try {
        const condensed = applyCompaction(plan, await summarize(plan.older));
        return { messages: condensed, estimate: shrink(messages, condensed, estimate) };
      } catch (error) {
        console.error("[misty-agent-runtime] context compaction failed", error);
        return undefined;
      }
    },
  };
}

/** Runs the condensers in order. The estimate starts from the last call's measured input. */
export async function condense(
  messages: ModelMessage[],
  measured: { inputTokens: number; sentBytes: number; window: number },
  condensers: Condenser[],
): Promise<ModelMessage[]> {
  let estimate = measured.inputTokens > 0 ? estimateInputTokens(measured.inputTokens, measured.sentBytes, messages) : messageBytes(messages) / 4;
  let current = messages;
  for (const step of condensers) {
    const result = await step.condense(current, { estimate, window: measured.window });
    if (result) ({ messages: current, estimate } = result);
  }
  return current;
}
