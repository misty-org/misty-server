// Checkpoints: a task resumed in the same conversation from where an earlier run stopped.
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { LedgerRow } from "../../src/ledger.js";
import { env } from "./config.js";
import type { OdysseysRun } from "./results.js";

/** Where an earlier run of a task stopped: what it answered and the conversation to continue. */
export interface Checkpoint {
  dir: string;
  taskId: string;
  answer: string;
  invocationId: string;
  conversationId: string;
  seconds: number;
  ledger: LedgerRow[];
}

/** A run's conversation, from the local stack's database. */
export function conversationOf(invocationId: string): string {
  if (!/^invocation_[0-9a-f-]{36}$/.test(invocationId)) return "";
  const container = env("MISTY_ACCEPTANCE_DB_CONTAINER", "misty-server-postgres-1");
  return execFileSync("docker", ["exec", container, "psql", "-U", "misty", "-d", "misty_server", "-t", "-A", "-c",
    `SELECT coalesce(conversation_id,'') FROM ai_invocations WHERE id='${invocationId}'`], { encoding: "utf8" }).trim();
}

export function checkpointAt(dir: string): Checkpoint {
  const file = join(dir, "result.json");
  if (!existsSync(file)) throw new Error(`No result.json in ${dir}: only a graded run can be resumed.`);
  const run = JSON.parse(readFileSync(file, "utf8")) as Partial<OdysseysRun> & { taskId: string };
  const invocationId = run.invocationId ?? "";
  const conversationId = run.conversationId || conversationOf(invocationId);
  if (!conversationId) throw new Error(`${dir} has no conversation to continue (result.json needs its invocationId).`);
  return { dir, taskId: run.taskId, answer: run.answer ?? "", invocationId, conversationId, seconds: run.seconds ?? 0, ledger: run.ledger ?? [] };
}

/** The turn that resumes a task: what remains, then the whole answer again. */
export const resumePrompt =
  "Continue the task above from where you stopped. Your last answer says what you found and what was not done: do what remains, " +
  "and make sure every page the request asks to keep open is open in its own tab. Then give the complete final answer for the whole " +
  "request, including what you found before.";
