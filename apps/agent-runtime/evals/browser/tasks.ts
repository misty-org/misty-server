// Loads and checks the browser eval's tasks, and serves its fixture sites.
import { readFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { extname, join, normalize, resolve } from "node:path";

export interface EvalTask {
  id: string;
  category: string;
  app: string;
  prompt: string;
  start: { site?: string; url?: string; urlEnv?: string };
  requires?: string[];
  /** Runs in the page's own world after the run; must return true. */
  check?: string;
  /** Every string must appear in the final answer. */
  expect?: string[];
  /** Graded by the judge model. */
  rubric?: string[];
  injection?: boolean;
  maxSteps?: number;
}

const here = resolve(import.meta.dirname);
const sitesDir = join(here, "sites");

export function validateTask(task: EvalTask): EvalTask {
  if (!/^[a-z0-9-]{3,60}$/.test(task.id)) throw new Error(`bad task id ${task.id}`);
  if (!task.prompt?.trim() || !task.category || !task.app) throw new Error(`${task.id}: prompt, category and app are required`);
  const starts = [task.start?.site, task.start?.url, task.start?.urlEnv].filter(Boolean);
  if (starts.length !== 1) throw new Error(`${task.id}: give exactly one of start.site, start.url, start.urlEnv`);
  if (!task.check && !task.expect?.length && !task.rubric?.length) throw new Error(`${task.id}: needs a check, expect or rubric`);
  if (task.start.site && task.requires?.length) throw new Error(`${task.id}: fixture tasks need no accounts`);
  return task;
}

export function loadTasks(file = join(here, "tasks.json")): EvalTask[] {
  const tasks = (JSON.parse(readFileSync(file, "utf8")) as { tasks: EvalTask[] }).tasks.map(validateTask);
  const ids = new Set<string>();
  for (const task of tasks) {
    if (ids.has(task.id)) throw new Error(`duplicate task ${task.id}`);
    ids.add(task.id);
  }
  return tasks;
}

/** Replaces {{NAME}} with the environment variable NAME. */
export function fillPlaceholders(text: string, env: Record<string, string | undefined> = process.env): string {
  return text.replace(/\{\{([A-Z0-9_]+)\}\}/g, (_, name: string) => {
    const value = env[name]?.trim();
    if (!value) throw new Error(`missing ${name}`);
    return value;
  });
}

/**
 * Why a task cannot run in this environment, or "" when it can. Live tasks
 * need their account tags in MISTY_BROWSER_EVAL_ACCOUNTS and their URLs.
 */
export function skipReason(task: EvalTask, env: Record<string, string | undefined> = process.env): string {
  if (task.start.site) return "";
  const accounts = new Set((env.MISTY_BROWSER_EVAL_ACCOUNTS ?? "").split(",").map((item) => item.trim()).filter(Boolean));
  const missing = (task.requires ?? []).filter((tag) => !accounts.has(tag));
  if (missing.length) return `needs accounts: ${missing.join(", ")}`;
  if (task.start.urlEnv && !env[task.start.urlEnv]?.trim()) return `needs ${task.start.urlEnv}`;
  try {
    fillPlaceholders(task.prompt, env);
  } catch (error) {
    return `needs ${String((error as Error).message).replace("missing ", "")}`;
  }
  return "";
}

export function startUrl(task: EvalTask, siteBase: string, env: Record<string, string | undefined> = process.env): string {
  if (task.start.site) return `${siteBase}/${task.start.site}`;
  if (task.start.url) return task.start.url;
  return env[task.start.urlEnv!]!.trim();
}

const types: Record<string, string> = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css" };

/** Serves the fixture sites on a loopback port: real origins, real navigation. */
export async function serveSites(): Promise<{ base: string; close: () => Promise<void> }> {
  const server: Server = createServer((request, response) => {
    const path = normalize(decodeURIComponent(new URL(request.url ?? "/", "http://x").pathname)).replace(/^\/+/, "");
    const file = join(sitesDir, path || "search.html");
    if (!file.startsWith(sitesDir)) {
      response.writeHead(403).end();
      return;
    }
    try {
      const body = readFileSync(file);
      response.writeHead(200, { "Content-Type": types[extname(file)] ?? "application/octet-stream", "Cache-Control": "no-store" }).end(body);
    } catch {
      response.writeHead(404).end("Not found");
    }
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("no fixture server address");
  return {
    base: `http://127.0.0.1:${address.port}`,
    close: () => new Promise<void>((done) => server.close(() => done())),
  };
}
