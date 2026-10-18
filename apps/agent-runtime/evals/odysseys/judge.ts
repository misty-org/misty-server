// Odysseys' per-rubric judge, ported from scripts/python/run_full_trajectory_per_rubric.py
// (MIT, see data/NOTICE): the same system prompt and message, one call per rubric item,
// the trajectory's action history and every screenshot in order, and the same
// success/failure parse. A rubric scores 1 when the trajectory satisfies it; a task is
// perfect when every rubric does. Differences from the official runs are only the judge
// model (MISTY_ODYSSEYS_JUDGE, a Claude model on Misty's own key) and a cap on pictures.
import { readFileSync } from "node:fs";
import { generateText } from "ai";
import { RoutedModel } from "../../src/model-calls.js";
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

export interface RubricResult {
  id: string;
  success: boolean;
  reasoning: string;
}

export interface TaskVerdict {
  rubrics: RubricResult[];
  average: number;
  perfect: boolean;
  inputTokens: number;
  outputTokens: number;
}

/** Grades every rubric of one task; the last `maxImages` screenshots go to each call. */
export async function judgeTask(model: string, task: OdysseysTask, steps: Step[], screenshots: string[], maxImages = 24): Promise<TaskVerdict> {
  const history = steps.map((step, index) => {
    const parts = [step.response.trim() ? `Response: ${step.response.trim()}` : "", step.action ? `Action: ${step.action}` : ""].filter(Boolean);
    return parts.length ? `${index + 1}. ${parts.join("\n")}` : "";
  }).filter(Boolean).join("\n") || "No actions recorded.";
  const images = screenshots.slice(-maxImages).map((file) => ({
    type: "file" as const, mediaType: file.endsWith(".png") ? "image/png" : "image/jpeg", data: readFileSync(file),
  }));
  let inputTokens = 0;
  let outputTokens = 0;
  const rubrics: RubricResult[] = [];
  for (const rubric of task.rubrics) {
    const lines = [`Rubric ID: ${rubric.id}`, `Requirement: ${rubric.requirement.trim()}`, ...(rubric.verification.trim() ? [`Verification: ${rubric.verification.trim()}`] : [])];
    const text = `User Task (context only): ${task.prompt.trim()}\n\nEvaluate ONLY this rubric item:\n${lines.join("\n")}` +
      `\n\nFull Action History:\n${history}\n\nScreenshots attached below: ${images.length} (trajectory had ${steps.length} total step(s)).` +
      `\n\nDecide whether the rubric (${rubric.id}) is satisfied. Use the required 'Thoughts:' / 'Status:' format.`;
    try {
      const result = await generateText({
        model: new RoutedModel(model, { provider: "instance", reasoning: "low" }),
        system, maxOutputTokens: 4_000, maxRetries: 2,
        messages: [{ role: "user", content: [{ type: "text" as const, text }, ...images] }],
      });
      inputTokens += result.usage.inputTokens ?? 0;
      outputTokens += result.usage.outputTokens ?? 0;
      const status = /Status:\s*["']?(success|failure)["']?/i.exec(result.text);
      const thoughts = /Thoughts:\s*([\s\S]+?)(?:Status:|$)/.exec(result.text);
      rubrics.push({ id: rubric.id, success: status?.[1]?.toLowerCase() === "success", reasoning: (thoughts?.[1] ?? result.text).trim().slice(0, 1_200) });
    } catch (error) {
      rubrics.push({ id: rubric.id, success: false, reasoning: `Error judging rubric ${rubric.id}: ${String(error).slice(0, 300)}` });
    }
  }
  const passed = rubrics.filter((item) => item.success).length;
  return { rubrics, average: rubrics.length ? passed / rubrics.length : 0, perfect: rubrics.length > 0 && passed === rubrics.length, inputTokens, outputTokens };
}
