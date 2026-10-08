import { expect, it } from "vitest";
import { expectPassed, passed, summarize, type TaskRun } from "../evals/browser/score.js";
import { fillPlaceholders, loadTasks, skipReason, startUrl, validateTask, type EvalTask } from "../evals/browser/tasks.js";

it("loads a benchmark that spans several apps per category", () => {
  const tasks = loadTasks();
  expect(tasks.length).toBeGreaterThanOrEqual(36);
  const apps = (category: string) => new Set(tasks.filter((task) => task.category === category).map((task) => task.app));
  for (const category of ["forms", "spreadsheets", "assignments", "work-apps", "mail", "research"]) expect(apps(category).size).toBeGreaterThanOrEqual(2);
  // Fixture tasks run anywhere: they are the reproducible core.
  expect(tasks.filter((task) => task.start.site).length).toBeGreaterThanOrEqual(15);
  expect(tasks.some((task) => task.injection)).toBe(true);
});

it("rejects malformed tasks", () => {
  const base: EvalTask = { id: "ok-task", category: "forms", app: "fixture", prompt: "Do it.", start: { site: "x.html" }, check: "true" };
  expect(validateTask(base)).toBe(base);
  expect(() => validateTask({ ...base, start: { site: "x.html", url: "https://x" } })).toThrow();
  expect(() => validateTask({ ...base, check: undefined })).toThrow();
  expect(() => validateTask({ ...base, requires: ["google"] })).toThrow();
  expect(() => validateTask({ ...base, id: "Bad Id" })).toThrow();
});

it("skips live tasks without their accounts and URLs", () => {
  const live: EvalTask = { id: "live", category: "mail", app: "gmail", prompt: "Reply in {{MISTY_EVAL_X}}.", start: { urlEnv: "MISTY_EVAL_URL" }, requires: ["google"], rubric: ["r"] };
  expect(skipReason(live, {})).toBe("needs accounts: google");
  expect(skipReason(live, { MISTY_BROWSER_EVAL_ACCOUNTS: "google" })).toBe("needs MISTY_EVAL_URL");
  expect(skipReason(live, { MISTY_BROWSER_EVAL_ACCOUNTS: "google", MISTY_EVAL_URL: "https://a" })).toBe("needs MISTY_EVAL_X");
  const env = { MISTY_BROWSER_EVAL_ACCOUNTS: "google", MISTY_EVAL_URL: "https://a", MISTY_EVAL_X: "Inbox" };
  expect(skipReason(live, env)).toBe("");
  expect(fillPlaceholders(live.prompt, env)).toBe("Reply in Inbox.");
  expect(startUrl(live, "http://127.0.0.1:1", env)).toBe("https://a");
});

const run = (extra: Partial<TaskRun>): TaskRun => ({
  taskId: "t", category: "forms", app: "fixture", model: "m", finished: true, ms: 1000, modelTurns: 3, toolCalls: 4,
  toolMs: 400, inputTokens: 10, cachedInputTokens: 0, outputTokens: 5, ...extra,
});

it("passes a run only when every verdict it has passes", () => {
  expect(passed(run({ checkPassed: true }))).toBe(true);
  expect(passed(run({ checkPassed: true, judgePassed: false }))).toBe(false);
  expect(passed(run({}))).toBe(false);
  expect(passed(run({ checkPassed: true, error: "boom" }))).toBe(false);
  expect(expectPassed("Code PX-60603.", ["px-60603"])).toBe(true);
  expect(expectPassed("nothing", ["PX-60603"])).toBe(false);
  expect(expectPassed("x", undefined)).toBeUndefined();
});

it("summarizes success, time and where it went", () => {
  const summary = summarize([run({ checkPassed: true, ms: 1000 }), run({ checkPassed: false, ms: 3000, followedInjection: true })]);
  expect(summary.successRate).toBe(0.5);
  expect(summary.medianMs).toBe(3000);
  expect(summary.toolShare).toBeCloseTo(0.2);
  expect(summary.injectionFollowed).toBe(1);
});
