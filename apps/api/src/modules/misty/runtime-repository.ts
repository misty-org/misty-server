import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { lockMistyAccount, lockMistyConversation } from "./access.js";

export type RuntimeNodeEvent = {
  runtime_run_id: string; node_id: string; state: "running" | "completed" | "failed";
  phase: string; output: Record<string, unknown>;
};
export type RuntimeInvocation = {
  id: string; user_id: string; space_id: string | null; conversation_id: string | null;
  state: string; runtime_kind: string; runtime_run_id: string; request_payload: Record<string, unknown>;
};
type StreamEvent = { type: string; [key: string]: unknown };
export class RuntimeCallbackConflict extends Error {}

/** The wire reports node progress. Only /complete may terminate an invocation. */
function projectNode(event: RuntimeNodeEvent): StreamEvent[] {
  if (event.node_id.startsWith("tool:")) {
    const toolName = event.phase.replace(/^using_/, "").replaceAll("_", ".");
    const label = toolName.trim().replaceAll("_", " ").replaceAll(".", " ");
    return [
      ...(event.state === "running" ? [{ type: "assistant.status", phase: "tool", text: label ? `Using ${label}…` : "Checking Misty…" }] : []),
      { type: event.state === "running" ? "tool.started" : event.state === "failed" ? "tool.failed" : "tool.completed",
        toolCallId: event.node_id.slice(5), toolName },
    ];
  }
  const delta = event.output.text_delta;
  return event.node_id.startsWith("model:") && event.state === "completed" && typeof delta === "string" && delta.trim()
    ? [{ type: "response.delta", delta }] : [];
}

export function createRuntimeCallbackRepository(pool: Pool, options: {
  // Same invocation key must reserve once across model nodes/retries. Runs outside
  // the callback SQL transaction so the usage repository keeps its own lock order.
  reserveModelUsage: (invocation: RuntimeInvocation) => Promise<void>;
}) {
  async function withInvocation<T>(id: string, operation: (tx: PoolClient, row: RuntimeInvocation) => Promise<T>): Promise<T | null> {
    return withTransaction(pool, async tx => {
      await tx.query("SET LOCAL lock_timeout='2s'");
      await tx.query("SET LOCAL statement_timeout='5s'");
      const parent = (await tx.query<RuntimeInvocation>(`SELECT id,user_id,space_id,conversation_id FROM ai_invocations
        WHERE id=$1 AND runtime_owner='hono'`, [id])).rows[0];
      if (!parent || !await lockMistyAccount(tx, parent.user_id, parent.space_id)
        || !await lockMistyConversation(tx, parent.user_id, parent.space_id, parent.conversation_id)) return null;
      const row = (await tx.query<RuntimeInvocation>(`SELECT id,user_id,space_id,conversation_id,state,runtime_kind,runtime_run_id,request_payload
        FROM ai_invocations WHERE id=$1 AND runtime_owner='hono'
        AND (expires_at IS NULL OR expires_at>clock_timestamp()) FOR UPDATE`, [id])).rows[0];
      if (!row || row.user_id !== parent.user_id || row.space_id !== parent.space_id || row.conversation_id !== parent.conversation_id) return null;
      return operation(tx, row);
    }, { mode: "service" });
  }
  async function received(tx: PoolClient, id: string, key: string, hash: string) {
    const receipt = (await tx.query<{ body_sha256: string }>(
      "SELECT body_sha256 FROM ai_runtime_callback_receipts WHERE invocation_id=$1 AND effect_key=$2", [id, key])).rows[0];
    if (receipt && receipt.body_sha256 !== hash) throw new RuntimeCallbackConflict("Runtime callback changed");
    return Boolean(receipt);
  }
  async function append(tx: PoolClient, row: RuntimeInvocation, events: StreamEvent[], key: string, hash: string) {
    const next = (await tx.query<{ sequence: string }>(
      "SELECT (COALESCE(MAX(sequence),0)+1)::text AS sequence FROM ai_invocation_events WHERE invocation_id=$1", [row.id])).rows[0]!;
    let sequence = BigInt(next.sequence);
    for (const event of events) {
      await tx.query(`INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,native_resulting_state)
        VALUES($1,$2,$3,$4::jsonb,$5)`, [row.id, sequence.toString(), event.type,
        JSON.stringify({ ...event, id: sequence.toString() }), row.state]);
      sequence++;
    }
    await tx.query("INSERT INTO ai_runtime_callback_receipts(invocation_id,effect_key,body_sha256) VALUES($1,$2,$3)", [row.id, key, hash]);
    const touched = await tx.query(`UPDATE ai_invocations SET runtime_heartbeat_at=clock_timestamp(),updated_at=now()
      WHERE id=$1 AND (expires_at IS NULL OR expires_at>clock_timestamp())`, [row.id]);
    if (!touched.rowCount) throw new RuntimeCallbackConflict("Invocation expired");
  }
  const active = (row: RuntimeInvocation, runId: string) => row.runtime_run_id === runId && ["running", "awaiting_approval"].includes(row.state);
  return {
    async activate(id: string, input: { runtime_run_id: string; runtime_kind: string }, hash: string) {
      return withInvocation(id, async (tx, row) => {
        if (!["queued", "running", "awaiting_approval"].includes(row.state)) return null;
        if (row.runtime_run_id && (row.runtime_run_id !== input.runtime_run_id || row.runtime_kind !== input.runtime_kind)) return null;
        if (!row.runtime_run_id && row.state !== "queued") return null;
        if (await received(tx, id, "activate", hash)) return { run_id: id, state: row.state };
        const changed = await tx.query(`UPDATE ai_invocations SET runtime_kind=$2,runtime_run_id=$3,
          state=CASE WHEN state='queued' THEN 'running' ELSE state END WHERE id=$1 RETURNING state`, [id, input.runtime_kind, input.runtime_run_id]);
        row.state = changed.rows[0].state;
        await append(tx, row, [{ type: "invocation.started", state: row.state }, { type: "assistant.status", phase: "thinking" }], "activate", hash);
        return { run_id: id, state: row.state };
      });
    },
    async event(id: string, event: RuntimeNodeEvent, hash: string) {
      // The idempotency header is not signed by the existing protocol. Derive the
      // effect key from signed node/state fields, so changing the header cannot
      // duplicate a tool notification, text delta or model reservation.
      const key = JSON.stringify(["events", event.node_id, event.state]);
      if (event.node_id.startsWith("model:") && event.state === "running") {
        const pending = await withInvocation(id, async (tx, row) => {
          if (!active(row, event.runtime_run_id)) return null;
          return { row, duplicate: await received(tx, id, key, hash) };
        });
        if (!pending) return null;
        if (pending.duplicate) return { accepted: true };
        await options.reserveModelUsage(pending.row);
      }
      return withInvocation(id, async (tx, row) => {
        if (!active(row, event.runtime_run_id)) return null;
        if (!await received(tx, id, key, hash)) await append(tx, row, projectNode(event), key, hash);
        return { accepted: true };
      });
    },
  };
}
