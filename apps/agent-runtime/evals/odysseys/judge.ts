// Odysseys' per-rubric judge, ported from scripts/python/run_full_trajectory_per_rubric.py
// (MIT, see data/NOTICE): the same system prompt and message, one call per rubric item,
// the trajectory's action history and every screenshot in order, and the same
// success/failure parse. A rubric scores 1 when the trajectory satisfies it; a task is
// perfect when every rubric does. Differences from the official runs are only the judge
// model (MISTY_ODYSSEYS_JUDGE: a Claude model on the instance's Anthropic key, or a free `nim/` model, see
// docs/agents/evals/free-model.md) and a cap of 60 pictures
// (the recorder skips unchanged ones, so long runs rarely reach it).
import { readFileSync } from "node:fs";
import { streamText } from "ai";
import { RoutedModel } from "../../src/model-calls.js";
import { judgeBu } from "./bu-bench.js";
import type { Step } from "./recorder.js";
import type { OdysseysTask } from "./tasks.js";

const system = `You are an expert evaluator of web-navigation agent trajectories.

You will receive:
- The user task (for context).
- ONE specific rubric item with a requirement and a verification description.
- The agent's full action history (one line per step).
- Every screenshot from the trajectory, in chronological order.

Your goal is to decide whether this single rubric item is satisfied by the trajectory.

Evaluation rules:
- Judge ONLY the one rubric item you are given; ignore all other implicit requirements.
- Ground your judgment in what the screenshots and actions actually show. Do not invent state.
- Filtering / sorting / form requirements must be applied and confirmed to count as satisfied.
- If the agent was blocked (captcha, access denied, etc.) and therefore could not satisfy the rubric, report failure.

Respond in exactly this format:

Thoughts: <your reasoning, citing specific steps/screenshots>
Status: "success" or "failure"
`;

interface RubricResult {
  id: string;
  /** The model that gave the verdict: the fallback when the judge model was down. */
  judgedBy?: string;
  success: boolean;
  reasoning: string;
}

interface TaskVerdict {
  rubrics: RubricResult[];
  average: number;
  perfect: boolean;
  inputTokens: number;
  outputTokens: number;
}

/** Rubrics judged at once on a free model (MISTY_ODYSSEYS_JUDGE_LANES): its rate limit is shared
 * with the agent, but tasks run one at a time, so grading has it to itself. */
const judgeLanes = Math.max(1, Math.min(8, Number(process.env.MISTY_ODYSSEYS_JUDGE_LANES || 5)));

/** Grades every rubric of one task; the last `maxImages` screenshots go to each call. */
export async function judgeTask(model: string, task: OdysseysTask, steps: Step[], screenshots: string[], maxImages = Number(process.env.MISTY_ODYSSEYS_JUDGE_IMAGES || 60)): Promise<TaskVerdict> {
  if (task.judge === "bu-v1") return judgeBu(model, task, steps, screenshots);
  const history = steps.map((step, index) => {
    const parts = [step.response.trim() ? `Response: ${step.response.trim()}` : "", step.action ? `Action: ${step.action}` : ""].filter(Boolean);
    return parts.length ? `${index + 1}. ${parts.join("\n")}` : "";
  }).filter(Boolean).join("\n") || "No actions recorded.";
  const images = screenshots.slice(-maxImages).map((file) => ({
    type: "file" as const, mediaType: file.endsWith(".png") ? "image/png" : "image/jpeg", data: readFileSync(file),
  }));
  let inputTokens = 0;
  let outputTokens = 0;
  // Each rubric item is judged on its own, as the original does; the calls run together,
  // except on a free model's rate limit (docs/agents/evals/free-model.md), where a few at a time
  // (judgeLanes) keep a 429 from being graded as a failed rubric.
  const free = /^(nim|nvidia|gemini)\//.test(model);
  const fallbackModel = process.env.MISTY_ODYSSEYS_JUDGE_FALLBACK?.trim() ?? "";
  const judgeOne = async (rubric: OdysseysTask["rubrics"][number]): Promise<RubricResult> => {
    // The judge's model, until it fails for its own reasons (a free tier's 503s last for hours): then
    // this rubric, and the next ones, are judged by the fallback model, and the result says so.
    let judge = model;
    const lines = [`Rubric ID: ${rubric.id}`, `Requirement: ${rubric.requirement.trim()}`, ...(rubric.verification.trim() ? [`Verification: ${rubric.verification.trim()}`] : [])];
    const text = `User Task (context only): ${task.prompt.trim()}\n\nEvaluate ONLY this rubric item:\n${lines.join("\n")}` +
      `\n\nFull Action History:\n${history}\n\nScreenshots attached below: ${images.length} (trajectory had ${steps.length} total step(s)).` +
      `\n\nDecide whether the rubric (${rubric.id}) is satisfied. Use the required 'Thoughts:' / 'Status:' format.`;
    // No clock ends a judge call; like a run (watchdog.ts), it is asked again only when it goes
    // quiet: no token, thinking included, for three minutes.
    const ask = async () => {
      const quiet = new AbortController();
      let heard = Date.now();
      const look = setInterval(() => Date.now() - heard > 180_000 && quiet.abort(), 15_000);
      try {
        const stream = streamText({
          model: new RoutedModel(judge, { provider: "instance", reasoning: "low" }),
          // A free model thinks before it answers; room for both, or the verdict can come back empty.
          system, maxOutputTokens: free ? 16_000 : 4_000, maxRetries: free && !fallbackModel ? 6 : 2, abortSignal: quiet.signal,
          messages: [{ role: "user", content: [{ type: "text" as const, text }, ...images] }],
          onChunk: () => { heard = Date.now(); },
        });
        const [answer, usage] = await Promise.all([stream.text, stream.usage]);
        return { text: answer, usage };
      } catch (error) {
        throw quiet.signal.aborted ? new Error("judge_quiet") : error;
      } finally {
        clearInterval(look);
      }
    };
    try {
      let result: Awaited<ReturnType<typeof ask>> | undefined;
      // A reply with no "Status:" line is no verdict at all (an empty answer is not a failed
      // rubric): ask again, twice at most.
      const verdict = (reply: { text: string }) => /Status:\s*["']?(success|failure)/i.test(reply.text);
      for (let attempt = 0; !result || (!verdict(result) && attempt < 3); attempt++) {
        result = await ask().catch(async (error: unknown) => {
          // A quiet call, a rate limit or a provider error says nothing about the run: wait
          // longer each time and ask again, rather than grading the agent on the judge's outage.
          if (attempt < 5 && /judge_quiet|429|rate.?limit|quota|exhaust|overload|unavailable|50\d|fetch failed/i.test(String(error))) {
            if (fallbackModel && judge !== fallbackModel) {
              process.stderr.write(`JUDGE_FALLBACK ${rubric.id}: ${judge} -> ${fallbackModel} (${String(error).slice(0, 160)})\n`);
              judge = fallbackModel;
              return undefined;
            }
            await new Promise((done) => setTimeout(done, 30_000 * (attempt + 1)));
            return undefined;
          }
          throw error;
        });
      }
      inputTokens += result.usage.inputTokens ?? 0;
      outputTokens += result.usage.outputTokens ?? 0;
      const status = /Status:\s*["']?(success|failure)["']?/i.exec(result.text);
      const thoughts = /Thoughts:\s*([\s\S]+?)(?:Status:|$)/.exec(result.text);
      return { id: rubric.id, judgedBy: judge, success: status?.[1]?.toLowerCase() === "success", reasoning: (thoughts?.[1] ?? result.text).trim().slice(0, 1_200) };
    } catch (error) {
      return { id: rubric.id, success: false, reasoning: `Error judging rubric ${rubric.id}: ${String(error).slice(0, 300)}` };
    }
  };
  const rubrics: RubricResult[] = new Array(task.rubrics.length);
  let next = 0;
  const lane = async () => {
    while (next < task.rubrics.length) {
      const index = next++;
      rubrics[index] = await judgeOne(task.rubrics[index]!);
    }
  };
  await Promise.all(Array.from({ length: free ? judgeLanes : task.rubrics.length }, lane));
  const passed = rubrics.filter((item) => item.success).length;
  return { rubrics, average: rubrics.length ? passed / rubrics.length : 0, perfect: rubrics.length > 0 && passed === rubrics.length, inputTokens, outputTokens };
}
