// Odysseys tasks (data/odysseys.json, MIT; see data/NOTICE) and the campaign's fixed
// splits (docs/agents/evals/odysseys.md): dev for every iteration, holdout for
// milestones, rest only in full passes. The split is a stable hash of each task id, per
// level, so it never changes between runs or machines.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { loadBuTasks } from "./bu-bench.js";

type Level = "easy" | "medium" | "hard";
export type Split = "dev" | "holdout" | "rest";

export interface Rubric {
  id: string;
  requirement: string;
  verification: string;
}

export interface OdysseysTask {
  id: string;
  prompt: string;
  startUrl: string;
  level: Level;
  /** A person's step count for the task. */
  referenceLength: number;
  rubrics: Rubric[];
  split: Split;
  /** BU Bench's one binary verdict (bu-bench.ts) instead of Odysseys' rubrics. */
  judge?: "bu-v1";
}

interface RawTask {
  task_id: string;
  confirmed_task: string;
  website: string;
  level: Level;
  reference_length: number;
  rubrics: Record<string, { requirement: string; verification: string }>;
}

/** Tasks per split and level: dev 40 and holdout 60, in the benchmark's own proportions. */
const quota: Record<Exclude<Split, "rest">, Record<Level, number>> = {
  dev: { easy: 9, medium: 9, hard: 22 },
  holdout: { easy: 13, medium: 14, hard: 33 },
};

const rank = (id: string) => createHash("sha1").update(`misty-odysseys-split-v1:${id}`).digest("hex");

export function loadTasks(): OdysseysTask[] {
  if (process.env.MISTY_ODYSSEYS_DATASET === "bu-v1") return loadBuTasks();
  const raw = JSON.parse(readFileSync(resolve(import.meta.dirname, "data/odysseys.json"), "utf8")) as RawTask[];
  const tasks: OdysseysTask[] = [];
  for (const level of ["easy", "medium", "hard"] as const) {
    const ordered = raw.filter((task) => task.level === level).sort((a, b) => rank(a.task_id).localeCompare(rank(b.task_id)));
    ordered.forEach((task, index) => {
      const split: Split = index < quota.dev[level] ? "dev" : index < quota.dev[level] + quota.holdout[level] ? "holdout" : "rest";
      tasks.push({
        id: task.task_id, prompt: task.confirmed_task, startUrl: task.website, level, referenceLength: task.reference_length, split,
        rubrics: Object.entries(task.rubrics).map(([id, rubric]) => ({ id, requirement: rubric.requirement, verification: rubric.verification })),
      });
    });
  }
  // A stable order inside each split, mixing levels, so a batch is never all easy or all hard.
  return tasks.sort((a, b) => rank(a.id).localeCompare(rank(b.id)));
}

/** Tasks for a run: a split (dev, holdout, rest, all) or explicit ids or id prefixes. */
export function selectTasks(tasks: OdysseysTask[], split: string, ids: string[]): OdysseysTask[] {
  if (ids.length) return tasks.filter((task) => ids.some((id) => task.id.startsWith(id)));
  return split === "all" ? tasks : tasks.filter((task) => task.split === split);
}
