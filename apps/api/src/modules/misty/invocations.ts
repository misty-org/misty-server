import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { lockMistyAccount, lockMistyConversation } from "./access.js";

type RuntimeEvent = { userId: string; invocationId: string; runtimeKind: string; runtimeRunId: string;
  sequence: number; eventType: string; payload: Record<string, unknown>;
  state: "running" | "awaiting_approval" | "completed" | "failed" | "canceled" };

/** Internal runtime persistence boundary. The future HTTP adapter must verify
 * the runtime signature first; request JSON cannot supply an authenticated user. */
export function createMistyInvocationRepository(pool: Pool) {
  return { async appendRuntimeEvent(event: RuntimeEvent): Promise<boolean> {
    if (!Number.isSafeInteger(event.sequence) || event.sequence < 1 || !event.eventType || event.eventType.length > 80
      || !event.runtimeKind || !event.runtimeRunId || event.runtimeKind.length > 128 || event.runtimeRunId.length > 512
      || !['running','awaiting_approval','completed','failed','canceled'].includes(event.state)) throw new Error("Invalid runtime event");
    const payload = JSON.stringify(event.payload);
    if (Buffer.byteLength(payload) > 2 * 1024 * 1024) throw new Error("Runtime event exceeds limit");
    return withTransaction(pool, async tx => {
      await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
      // Discover the immutable parent before locking in Space -> account order.
      const parent = (await tx.query<{ space_id: string | null; conversation_id: string | null }>("SELECT space_id,conversation_id FROM ai_invocations WHERE id=$1 AND user_id=$2", [event.invocationId,event.userId])).rows[0];
      if (!parent) return false;
      if (!await lockMistyAccount(tx,event.userId,parent.space_id)) return false;
      if (!await lockMistyConversation(tx,event.userId,parent.space_id,parent.conversation_id)) return false;
      const invocation = (await tx.query<{ state: string; space_id: string | null; conversation_id: string | null }>(`SELECT state,space_id,conversation_id FROM ai_invocations WHERE id=$1 AND user_id=$2
        AND runtime_kind=$3 AND runtime_run_id=$4 AND (expires_at IS NULL OR expires_at>clock_timestamp()) FOR UPDATE`,
      [event.invocationId,event.userId,event.runtimeKind,event.runtimeRunId])).rows[0];
      if (!invocation || invocation.space_id !== parent.space_id || invocation.conversation_id !== parent.conversation_id) return false;
      const prior = await tx.query(`SELECT event_type=$3 AND payload=$4::jsonb AND native_resulting_state=$5 AS matches FROM ai_invocation_events
        WHERE invocation_id=$1 AND sequence=$2`, [event.invocationId,event.sequence,event.eventType,payload,event.state]);
      // Exact transport replay can acknowledge a terminal event without changing
      // state. A conflicting sequence can never replace committed content.
      if (prior.rowCount) return prior.rows[0].matches === true;
      if (!['running','awaiting_approval'].includes(invocation.state)) return false;
      const next = (await tx.query<{ sequence: string }>("SELECT (COALESCE(MAX(sequence),0)+1)::text AS sequence FROM ai_invocation_events WHERE invocation_id=$1", [event.invocationId])).rows[0]!;
      if (next.sequence !== String(event.sequence)) return false;
      await tx.query("INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,native_resulting_state) VALUES($1,$2,$3,$4::jsonb,$5)", [event.invocationId,event.sequence,event.eventType,payload,event.state]);
      const changed = await tx.query(`UPDATE ai_invocations SET state=$2,updated_at=now(),canceled_at=CASE WHEN $2='canceled' THEN now() ELSE canceled_at END
        WHERE id=$1 AND (expires_at IS NULL OR expires_at>clock_timestamp())`, [event.invocationId,event.state]);
      if (!changed.rowCount) throw new Error("Invocation expired during event commit");
      return true;
    }, { mode: "service" });
  } };
}
