// Composio tool calls through the signed-in person's own Misty session, the
// same connections Misty's agent uses. The seed script writes the account
// tasks' data with it; the runner reads the end state back after each run.
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { EvalClient } from "./client.js";

const serverDir = resolve(import.meta.dirname, "../../../..");

function envValue(names: string[], files: string[]): string {
  for (const file of files) {
    let text = "";
    try {
      text = readFileSync(resolve(serverDir, file), "utf8");
    } catch {
      continue;
    }
    for (const name of names) {
      const line = text.split("\n").find((item) => item.startsWith(`${name}=`));
      if (line) return line.slice(name.length + 1).replace(/^(['"])(.*)\1$/, "$2").trim();
    }
  }
  return "";
}

export type ComposioCall = (slug: string, args: Record<string, unknown>) => Promise<Record<string, unknown>>;

/** A caller for the signed-in person's Composio session, or an error naming what's missing. */
export async function composioFor(client: EvalClient): Promise<ComposioCall> {
  const key = envValue(["MISTY_COMPOSIO_API_KEY", "COMPOSIO_API_KEY"], [".env/dev/integrations/composio.env", ".env/dev/composio.env"]);
  if (!key) throw new Error("No Composio key in server/.env/dev.");
  const me = await client.json<{ id: string }>("/me");
  if (!/^[a-f0-9-]{36}$/.test(me.id)) throw new Error("unexpected user id");
  const session = execFileSync("docker", [
    "exec", process.env.MISTY_ACCEPTANCE_DB_CONTAINER ?? "misty-server-postgres-1", "psql", "-U", "misty", "-d", "misty_server", "-t", "-A",
    "-c", `SELECT session_id FROM composio_sessions WHERE owner_user_id='${me.id}'`,
  ]).toString().trim();
  if (!session) throw new Error("No Composio session; connect an app in Misty first.");
  return async (slug, args) => {
    const response = await fetch(`https://backend.composio.dev/api/v3.1/tool_router/session/${encodeURIComponent(session)}/execute`, {
      method: "POST",
      headers: { "x-api-key": key, "Content-Type": "application/json" },
      body: JSON.stringify({ tool_slug: slug, arguments: args }),
    });
    const body = (await response.json()) as { data?: Record<string, unknown>; error?: string | null };
    if (!response.ok || body.error) throw new Error(`${slug}: ${response.status} ${String(body.error ?? JSON.stringify(body)).slice(0, 300)}`);
    return body.data ?? {};
  };
}

/** The first string value named `field` anywhere in a result. */
export function find(value: unknown, field: string): string {
  if (!value || typeof value !== "object") return "";
  const record = value as Record<string, unknown>;
  if (typeof record[field] === "string") return record[field] as string;
  for (const item of Object.values(record)) {
    const found = find(item, field);
    if (found) return found;
  }
  return "";
}
