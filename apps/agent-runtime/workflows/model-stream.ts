import type { ModelMessage } from "ai";
import { sleep } from "workflow";
import type { fetchExecutionBudget } from "../src/control-plane-steps.js";
import { modelTimeout } from "../src/model-budget.js";
import { withCacheBreakpoint } from "../src/context/prompt-cache.js";
import { providerOutage } from "../src/runtime-errors.js";
import type { AgentRun } from "./agent-run.js";
import type { RunTools } from "./run-tools.js";
import type { Pacing } from "./turn-pacing.js";

/** How long a run waits out each provider outage before trying the same turn again. */
const OUTAGE_WAITS = ["30s", "60s", "120s", "240s"] as const;

type StreamOptions = Parameters<RunTools["agent"]["stream"]>[0];

/**
 * One model turn. A provider outage (src/runtime-errors.ts) is waited out, 30 s doubling to 4 min,
 * and the same turn tried again; the wait doesn't spend the run's time. Anything else still fails.
 */
export async function streamTurn(
  run: AgentRun,
  messages: ModelMessage[],
  shared: Pick<StreamOptions, "providerOptions" | "onAbort" | "runtimeContext">,
  budget: Awaited<ReturnType<typeof fetchExecutionBudget>>,
  pacing: Pacing,
  modelTurn: number,
) {
  for (let outage = 0; ; outage++) {
    try {
      return await run.tools.agent.stream({ messages: withCacheBreakpoint(messages, run.context.model_id), ...shared, timeout: modelTimeout(budget, Date.now()) });
    } catch (error) {
      if (outage >= OUTAGE_WAITS.length || run.hookFailure || !providerOutage(error)) throw error;
      console.log(`[misty-agent-runtime] provider_outage ${JSON.stringify({ run: run.identity.mistyRunId, turn: modelTurn, wait: OUTAGE_WAITS[outage] })}`);
      const waitStarted = Date.now();
      await sleep(OUTAGE_WAITS[outage]!);
      pacing.pausedMs += Date.now() - waitStarted;
    }
  }
}
