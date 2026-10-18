// Loads the workflow eval's tasks and turns their agent names into the tool
// names the harness has today (docs/plans/agent-harness.md keeps no aliases,
// so the translation lives here until phase 2 renames the tools).
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import type { StateCheck } from "./state.js";

export interface WorkflowTask {
  id: string;
  category: string;
  source: "polar" | "misty";
  app: "fixture" | "live";
  /** The agent.entry the request comes from. */
  surface: "composer" | "cursor" | "schedule";
  prompt: string;
  start?: { screen?: string; tab?: { site: string }; files?: string[] };
  requires?: string[];
  expect?: string[];
  rubric?: string[];
  tools?: { required?: string[]; forbidden?: string[]; maxCalls?: number; maxResearchCalls?: number; maxRuns?: number };
  /** What to read back after the run, for the judge (state.ts). */
  state?: StateCheck[];
  /** The answer key the judge checks the rubric against. */
  truth?: string;
  /** App actions (Composio slugs) the task asks for, whose approval cards the runner approves. */
  approve?: string[];
}

const here = resolve(import.meta.dirname);

export function loadTasks(): WorkflowTask[] {
  const tasks = (JSON.parse(readFileSync(join(here, "tasks.json"), "utf8")) as { tasks: WorkflowTask[] }).tasks;
  const ids = new Set<string>();
  for (const task of tasks) {
    if (!/^[a-z0-9-]{3,60}$/.test(task.id) || ids.has(task.id)) throw new Error(`bad or duplicate task id ${task.id}`);
    if (!task.prompt?.trim() || !["composer", "cursor", "schedule"].includes(task.surface)) throw new Error(`${task.id}: prompt and surface are required`);
    if (!task.expect?.length && !task.rubric?.length && !task.tools) throw new Error(`${task.id}: needs expect, rubric or tool rules`);
    ids.add(task.id);
  }
  return tasks;
}

/**
 * Agent names (as the model sees them) to the names the harness has today. A
 * null target is a tool that doesn't exist yet: required, it can't be met;
 * forbidden, it can't be broken. Tools took their agent names in phase 2.
 */
const today: Record<string, string | null> = {
  desktop_visual: "browser_workspace_visual",
};

/** Today's name for an agent name, or null when the tool doesn't exist yet. */
export function todayName(name: string): string | null {
  return name in today ? (today[name] ?? null) : name;
}

/** Whether a called tool matches a rule: exact, or a family ending in `*`. */
export function matches(rule: string, called: string): boolean {
  return rule.endsWith("*") ? called.startsWith(rule.slice(0, -1)) : called === rule;
}

/** Why a task can't run in this pass, or undefined. */
export function skipReason(task: WorkflowTask, accounts: Set<string>, device: boolean): string | undefined {
  const missing = (task.requires ?? []).filter((item) => item !== "desktop-control" && !accounts.has(item));
  if (missing.length) return `needs ${missing.join(", ")}`;
  if (task.requires?.includes("desktop-control") && !device) return "needs a desktop device";
  if (task.start?.screen && !existsSync(join(here, task.start.screen))) return `fixture ${task.start.screen} not built`;
  if (task.start?.files?.some((file) => !existsSync(join(here, file)))) return "fixture files not built";
  if (task.app === "fixture" && task.category === "research") return "fixture search index not built";
  return undefined;
}

/** A fixture screenshot as the cursor's display capture (ai_invocation_attachments.go). */
export function displayCapture(file: string) {
  const bytes = readFileSync(join(here, file));
  if (bytes.length > 1 << 20) throw new Error(`${file} is over the 1 MB capture limit`);
  // PNG: width and height are big-endian at bytes 16 and 20.
  const width = bytes.readUInt32BE(16);
  const height = bytes.readUInt32BE(20);
  return {
    id: `fixture-${createHash("sha256").update(file).digest("hex").slice(0, 16)}`,
    name: file.split("/").pop(),
    mime_type: "image/png",
    data_url: `data:image/png;base64,${bytes.toString("base64")}`,
    width,
    height,
    content_hash: createHash("sha256").update(bytes).digest("hex"),
    captured_at: Date.now(),
    screen: "screen1",
    primary: true,
  };
}
