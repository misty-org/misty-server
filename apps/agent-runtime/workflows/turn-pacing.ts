import type { ModelMessage } from "ai";
import { sleep } from "workflow";
import { takeSteering } from "../src/control-plane-steps.js";
import { messageBytes } from "../src/context/context-compaction.js";
import { loopNote } from "../src/loop-guard.js";
import { afterTakeover, TAKEOVER_LIMIT, TAKEOVER_POLL } from "../src/person-takeover.js";
import { budgetScale, closedResearchTools, researchNote, researchUse } from "../src/research-budget.js";
import { taskCompletionTool } from "../src/task-completion.js";
import { estimateMinutes, stillProgressing, timeBudget } from "../src/time-budget.js";
import { deadlineNote } from "../src/turn-deadline.js";
import type { AgentRun } from "./agent-run.js";

/** The run's clock and research spend, kept across turns and summaries. */
export interface Pacing {
  startedAt: number;
  // Time the run waited on the person or a provider, which doesn't spend the agent's time.
  pausedMs: number;
  // The agent's own estimate of the task's minutes (its first checklist), kept past compaction.
  estimate?: number;
  // The largest research budget the run has had (src/research-budget.ts), kept across summaries.
  researchScale: number;
}

/** Taken over (src/person-takeover.ts): wait without model turns until the person resumes. */
export async function waitOutTakeover(run: AgentRun, messages: ModelMessage[], pacing: Pacing): Promise<ModelMessage[]> {
  const sent: string[] = [];
  let paused = true;
  const pauseStarted = Date.now();
  for (let poll = 1; paused && poll <= TAKEOVER_LIMIT; poll++) {
    await sleep(TAKEOVER_POLL);
    const next = await takeSteering(run.identity, `${run.modelTurn}:paused:${poll}`, false);
    for (const message of next.messages) sent.push(message.text);
    paused = Boolean(next.paused);
  }
  // The person's turn in the window doesn't spend the agent's time.
  pacing.pausedMs += Date.now() - pauseStarted;
  return afterTakeover(messages, sent);
}

/** The notes a turn carries and the tools it may still use, from what the run has spent. */
export function paceTurn(run: AgentRun, messages: ModelMessage[], pacing: Pacing, visible: string[]): string[] {
  const { researchTools } = run.tools;
  const { modelTurn, limit } = run;
  // Research budget (src/research-budget.ts): a run that keeps searching is told what it
  // has spent, then loses search and fetch so its next turn can only answer.
  pacing.researchScale = Math.max(pacing.researchScale, budgetScale(messages));
  const research = researchUse(messages, researchTools, pacing.researchScale);
  const closed = closedResearchTools(research, researchTools);
  if (closed.size) visible = visible.filter((name) => !closed.has(name));
  const researchWarning = researchNote(research, messages, researchTools);
  if (researchWarning) messages.push({ role: "user", content: researchWarning });
  // The loop guard (src/loop-guard.ts): the same call or address three times brings a note.
  const loop = loopNote(messages);
  if (loop) messages.push({ role: "user", content: loop });
  // The deliverable deadline (src/turn-deadline.ts): near the turn limit the run writes its
  // answer from what it found instead of ending with nothing.
  const deadline = deadlineNote(modelTurn, limit, messages);
  if (deadline) messages.push({ role: "user", content: deadline });
  // The person's time (src/time-budget.ts), against the agent's own estimate: paced at
  // 80%, wrapping up at 100%, only the answer past about 130%, unless the run is still
  // finding new things.
  pacing.estimate = estimateMinutes(messages) ?? pacing.estimate;
  const time = timeBudget(Date.now() - pacing.startedAt - pacing.pausedMs, messages, pacing.estimate, stillProgressing(messages));
  if (time.note) messages.push({ role: "user", content: time.note });
  if (modelTurn >= limit - 1 || time.answerOnly)
    visible = visible.filter((name) => name === taskCompletionTool);
  return visible;
}

/** Tool definitions come first in the request, so a change costs this turn its cached prefix. */
export function logToolChange(run: AgentRun, lastToolKey: string): string {
  const toolKey = run.visibleTools.join(",");
  if (run.modelTurn > 0 && toolKey !== lastToolKey) {
    const before = new Set(lastToolKey.split(","));
    const now = new Set(run.visibleTools);
    console.log(`[misty-agent-runtime] tools_changed ${JSON.stringify({ run: run.identity.mistyRunId, turn: run.modelTurn, added: run.visibleTools.filter((name) => !before.has(name)), removed: [...before].filter((name) => !now.has(name)) })}`);
  }
  return toolKey;
}

/** What the first turn carries (docs/agents/harness.md, Context): the fixed cost of every turn. */
export function logContextBreakdown(run: AgentRun, messages: ModelMessage[]) {
  const { catalog, instructions } = run.tools;
  // The tools this turn sends, not the whole catalog: the rest wait behind agent_tool_use and loads.
  const sent = new Set(run.visibleTools);
  const toolSizes = catalog.tools.filter((tool) => sent.has(tool.modelName)).map((tool) => [tool.modelName, JSON.stringify(tool).length] as const);
  console.log(`[misty-agent-runtime] context_breakdown ${JSON.stringify({
    run: run.identity.mistyRunId,
    systemChars: instructions.length,
    toolChars: toolSizes.reduce((sum, [, size]) => sum + size, 0),
    tools: run.visibleTools.length,
    catalogTools: catalog.tools.length,
    toolSizes: [...toolSizes].sort((a, b) => b[1] - a[1]),
    messageChars: messageBytes(messages),
  })}`);
}
