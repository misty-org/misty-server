import type { ModelMessage } from "ai";
import { applyCompaction, CLEAR_AT, clearOldToolResults, COMPACT_AT, estimateInputTokens, messageBytes, planCompaction } from "./context-compaction.js";
import { pruneStalePageSnapshots } from "./prompt-cache.js";
import { CUT_BATCH_CHARS, cutOldResults, WORKING_TOKENS } from "./working-set.js";

// One condenser interface (docs/agents/harness.md, Context): every way the
// runtime keeps a transcript inside the model's window is a step with the same
// shape, run in order from the cheapest and most cache-friendly to the most
// lossy. Adding a step means adding it here, not in the agent loop.

interface ContextPressure {
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

/**
 * The working set (src/context/working-set.ts): above a fixed budget, not a share of the window, old
 * results are cut to digests and notes, so every turn stays small and fast.
 */
export const workingSet: Condenser = {
  name: "working_set",
  async condense(messages, { estimate }) {
    if (estimate <= WORKING_TOKENS) return undefined;
    const trimmed = cutOldResults(messages);
    if (!trimmed.cut || messageBytes(messages) - messageBytes(trimmed.messages) < CUT_BATCH_CHARS) return undefined;
    return { messages: trimmed.messages, estimate: shrink(messages, trimmed.messages, estimate) };
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

/** How many turns pass between summaries of older steps (Webwright compacts every 20 steps). */
export const SUMMARY_EVERY_TURNS = 20;

/**
 * A summary of older steps every SUMMARY_EVERY_TURNS turns once the transcript is past the
 * working budget (docs/agents/harness.md, Context): one rewrite on a schedule, then
 * twenty turns that only append, so the prompt cache holds between summaries.
 */
export function periodicSummary(summarize: (older: ModelMessage[]) => Promise<string>, due: () => boolean, done: () => void): Condenser {
  return {
    name: "periodic_summary",
    async condense(messages, { estimate }) {
      if (!due() || estimate <= WORKING_TOKENS) return undefined;
      const plan = planCompaction(messages);
      if (!plan) return undefined;
      try {
        const condensed = applyCompaction(plan, await summarize(plan.older));
        done();
        return { messages: condensed, estimate: shrink(messages, condensed, estimate) };
      } catch (error) {
        console.error("[misty-agent-runtime] periodic summary failed", error);
        return undefined;
      }
    },
  };
}

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
  // Never below the transcript's own size: with prompt caching a provider may report only the
  // uncached part of the last input, which left every budget below its threshold and let a run's
  // context grow past 100,000 tokens.
  const floor = messageBytes(messages) / 4;
  let estimate = measured.inputTokens > 0 ? Math.max(floor, estimateInputTokens(measured.inputTokens, measured.sentBytes, messages)) : floor;
  let current = messages;
  for (const step of condensers) {
    const result = await step.condense(current, { estimate, window: measured.window });
    if (!result) continue;
    // Each condenser that changes the transcript costs the next call its cached prefix.
    console.log(`[misty-agent-runtime] condensed ${JSON.stringify({ step: step.name, beforeChars: messageBytes(current), afterChars: messageBytes(result.messages) })}`);
    ({ messages: current, estimate } = result);
  }
  return current;
}
