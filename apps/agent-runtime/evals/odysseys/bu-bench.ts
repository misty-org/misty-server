// BU Bench V1 (browser-use/benchmark: 100 tasks, 20 each from WebBench, Online-Mind2Web 2,
// interaction tests, GAIA and BrowseComp) through the same product path as Odysseys. Its tasks
// ship encrypted so their text stays out of crawlers and training data: they are decrypted here
// in memory from a local checkout (MISTY_BU_BENCH_DIR) and never written into this repository.
// The judge is BU's own binary trajectory judge, its system prompt read from that checkout's
// judge.py at run time; reports stay in the git-ignored reports folder.
import { createDecipheriv, createHash, createHmac, timingSafeEqual } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { streamText } from "ai";
import { RoutedModel } from "../../src/model-calls.js";
import type { Step } from "./recorder.js";
import type { OdysseysTask } from "./tasks.js";

const benchmark = "BU_Bench_V1";
const dir = () => {
  const value = process.env.MISTY_BU_BENCH_DIR?.trim();
  if (!value) throw new Error("BU Bench needs a local checkout of browser-use/benchmark: set MISTY_BU_BENCH_DIR.");
  return value;
};

/** Fernet, keyed by sha256 of the benchmark's name, as BU's evaluation.py decrypts it. */
function decrypt(file: string, name: string): unknown {
  const key = createHash("sha256").update(name).digest();
  const token = Buffer.from(Buffer.from(readFileSync(file, "utf8").trim(), "base64").toString("utf8"), "base64url");
  const body = token.subarray(0, -32);
  const mac = createHmac("sha256", key.subarray(0, 16)).update(body).digest();
  if (!timingSafeEqual(mac, token.subarray(-32))) throw new Error(`${file} did not decrypt: is it ${name}?`);
  const decipher = createDecipheriv("aes-128-cbc", key.subarray(16), body.subarray(9, 25));
  return JSON.parse(Buffer.concat([decipher.update(body.subarray(25)), decipher.final()]).toString("utf8"));
}

/** The runner's own grouping by source, not BU's (it has no levels): lookups and interaction
 * tests first, live multi-step tasks next, deep research last. */
const levels: Record<string, OdysseysTask["level"]> = {
  WebBenchREAD: "easy", InteractionTests: "easy", OM2W2: "medium", GAIA: "hard", BrowseComp: "hard",
};

export function loadBuTasks(): OdysseysTask[] {
  const raw = decrypt(join(dir(), `${benchmark}.enc`), benchmark) as Array<{ task_id: string; confirmed_task: string; category: string }>;
  return raw.map((task) => ({
    id: String(task.task_id), prompt: task.confirmed_task, startUrl: "", level: levels[task.category] ?? "medium",
    referenceLength: 0, rubrics: [{ id: "verdict", requirement: task.category, verification: "" }], split: "rest", judge: "bu-v1",
  }));
}

/** BU's judge system prompt, with no ground truth (V1 has none). */
function judgeSystem(): string {
  const source = readFileSync(join(dir(), "judge.py"), "utf8");
  const prompt = /system_prompt = f"""([\s\S]*?)"""/.exec(source)?.[1];
  if (!prompt) throw new Error("judge.py has no system_prompt: the BU checkout changed shape");
  return prompt.replace("{ground_truth_section}", "").replaceAll("{{", "{").replaceAll("}}", "}");
}

const truncate = (text: string, length: number) => (text.length <= length ? text : `${text.slice(0, length - 20)}...[truncated]...`);

/** One binary verdict on the whole trajectory: the last ten distinct pictures, the steps and the answer. */
export async function judgeBu(model: string, task: OdysseysTask, steps: Step[], screenshots: string[]) {
  const answer = [...steps].reverse().find((step) => step.action === "final_answer")?.response ?? "";
  const history = steps.filter((step) => step.action !== "final_answer")
    .map((step, index) => `Step ${index + 1}: ${[step.action, step.response.trim()].filter(Boolean).join(" — ")}`).join("\n");
  const seen = new Set<string>();
  const pictures = [...screenshots].reverse().filter((file) => {
    const hash = createHash("sha1").update(readFileSync(file)).digest("hex");
    return !seen.has(hash) && Boolean(seen.add(hash));
  }).slice(0, 10).reverse();
  const text = `\n<task>\n${truncate(task.prompt, 40_000) || "No task provided"}\n</task>\n\n<agent_trajectory>\n${truncate(history, 40_000) || "No agent trajectory provided"}\n</agent_trajectory>\n\n<final_result>\n${truncate(answer, 40_000) || "No final result provided"}\n</final_result>\n\n${pictures.length} screenshots from execution are attached.\n\nEvaluate this agent execution given the criteria and respond with the exact JSON structure requested.`;
  const free = /^(nim|nvidia|gemini)\//.test(model);
  let reasoning = "";
  let success = false;
  let usage = { inputTokens: 0, outputTokens: 0 };
  for (let attempt = 0; attempt < 4; attempt++) {
    try {
      const stream = streamText({
        model: new RoutedModel(model, { provider: "instance", reasoning: "low" }),
        system: judgeSystem(), maxOutputTokens: free ? 16_000 : 4_000, maxRetries: 3,
        messages: [{ role: "user", content: [{ type: "text" as const, text }, ...pictures.map((file) => ({ type: "file" as const, mediaType: file.endsWith(".png") ? "image/png" : "image/jpeg", data: readFileSync(file) }))] }],
      });
      const [reply, used] = await Promise.all([stream.text, stream.usage]);
      usage = { inputTokens: usage.inputTokens + (used.inputTokens ?? 0), outputTokens: usage.outputTokens + (used.outputTokens ?? 0) };
      const json = /\{[\s\S]*\}/.exec(reply)?.[0];
      const parsed = json ? (JSON.parse(json) as { verdict?: unknown; reasoning?: string; failure_reason?: string }) : undefined;
      if (typeof parsed?.verdict !== "boolean") continue;
      success = parsed.verdict;
      reasoning = (success ? parsed.reasoning : parsed.failure_reason || parsed.reasoning) ?? "";
      break;
    } catch (error) {
      reasoning = `Error judging: ${String(error).slice(0, 300)}`;
      await new Promise((done) => setTimeout(done, 20_000 * (attempt + 1)));
    }
  }
  return {
    rubrics: [{ id: "verdict", judgedBy: model, success, reasoning: reasoning.slice(0, 1_200) }],
    average: success ? 1 : 0, perfect: success, ...usage,
  };
}
