import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { lockMistyAccount, lockMistyConversation } from "./access.js";

const inputSchema = z.object({ spaceId: z.string().min(1).max(200).nullable().default(null),
  conversationId: z.string().min(1).max(200).nullable().default(null), surfaceId: z.string().min(1).max(80),
  mode: z.enum(['quick','drawer','companion']), trigger: z.enum(['message','selection','object','schedule','event','handoff']),
  idempotencyKey: z.string().min(1).max(200), payload: z.record(z.string(),z.unknown()) }).strict();
export type InvocationInput = z.input<typeof inputSchema>;
export class MistyAdmissionError extends Error {
  constructor(readonly code: 'unavailable' | 'idempotency_conflict' | 'invalid_request') { super(code); }
}
type Invocation = { id: string; user_id: string; space_id: string | null; conversation_id: string | null; surface_id: string;
  mode: string; trigger_kind: string; state: string; runtime_kind: string; runtime_run_id: string; expires_at: Date | null };
const columns = 'id,user_id,space_id,conversation_id,surface_id,mode,trigger_kind,state,runtime_kind,runtime_run_id,expires_at';

/** Account-session admission only. Scheduled work needs a separate trusted
 * scheduler adapter; downloaded App credentials cannot impersonate a session. */
export function createMistyAdmissionRepository(pool: Pool) {
  return {
    async create(actor: { userId: string; sessionHash: string }, raw: InvocationInput): Promise<{ invocation: Invocation; created: boolean }> {
      const input = inputSchema.parse(raw), payload = JSON.stringify(input.payload);
      if (Buffer.byteLength(payload) > 2*1024*1024) throw new MistyAdmissionError('invalid_request');
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        if (!await lockMistyAccount(tx,actor.userId,input.spaceId,true)) throw new MistyAdmissionError('unavailable');
        const session = () => tx.query("SELECT token_hash FROM sessions WHERE user_id=$1 AND token_hash=$2 AND expires_at>clock_timestamp() FOR SHARE", [actor.userId,actor.sessionHash]);
        if (!(await session()).rowCount) throw new MistyAdmissionError('unavailable');
        if (!await lockMistyConversation(tx,actor.userId,input.spaceId,input.conversationId)) throw new MistyAdmissionError('unavailable');
        const inserted = await tx.query<Invocation>(`INSERT INTO ai_invocations(id,user_id,space_id,conversation_id,surface_id,mode,trigger_kind,state,idempotency_key,request_payload,expires_at,runtime_owner)
          VALUES($1,$2,$3,$4,$5,$6,$7,'queued',$8,$9::jsonb,clock_timestamp()+interval '24 hours','hono') ON CONFLICT(user_id,idempotency_key) DO NOTHING RETURNING ${columns}`,
        [`invocation_${randomUUID()}`,actor.userId,input.spaceId,input.conversationId,input.surfaceId,input.mode,input.trigger,input.idempotencyKey,payload]);
        let invocation = inserted.rows[0];
        if (!invocation) {
          const existing = (await tx.query<Invocation & { matches: boolean }>(`SELECT ${columns},
            space_id IS NOT DISTINCT FROM $3::text AND conversation_id IS NOT DISTINCT FROM $4::text AND surface_id=$5
            AND mode=$6 AND trigger_kind=$7 AND request_payload=$8::jsonb AND runtime_owner='hono' AS matches
            FROM ai_invocations WHERE user_id=$1 AND idempotency_key=$2 FOR UPDATE`,
          [actor.userId,input.idempotencyKey,input.spaceId,input.conversationId,input.surfaceId,input.mode,input.trigger,payload])).rows[0];
          if (!existing?.matches) throw new MistyAdmissionError('idempotency_conflict');
          const { matches: _, ...record } = existing; invocation = record;
        }
        // Row locks prevent revocation, but expiry advances independently.
        if (!(await session()).rowCount) throw new MistyAdmissionError('unavailable');
        return { invocation, created: inserted.rowCount === 1 };
      }, { mode: 'service' });
    },
    async activate(identity: { userId: string; invocationId: string; runtimeKind: string; runtimeRunId: string }): Promise<Invocation | null> {
      if (!identity.runtimeKind || identity.runtimeKind.length>128 || !identity.runtimeRunId || identity.runtimeRunId.length>512) throw new MistyAdmissionError('invalid_request');
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const parent = (await tx.query<{ space_id: string | null; conversation_id: string | null }>("SELECT space_id,conversation_id FROM ai_invocations WHERE id=$1 AND user_id=$2", [identity.invocationId,identity.userId])).rows[0];
        if (!parent || !await lockMistyAccount(tx,identity.userId,parent.space_id)) return null;
        if (!await lockMistyConversation(tx,identity.userId,parent.space_id,parent.conversation_id)) return null;
        const current = (await tx.query<Invocation>(`SELECT ${columns} FROM ai_invocations WHERE id=$1 AND user_id=$2
          AND (expires_at IS NULL OR expires_at>clock_timestamp()) FOR UPDATE`, [identity.invocationId,identity.userId])).rows[0];
        if (!current || current.space_id!==parent.space_id || current.conversation_id!==parent.conversation_id || !['queued','running','awaiting_approval'].includes(current.state)) return null;
        if (current.runtime_run_id && (current.runtime_run_id!==identity.runtimeRunId || current.runtime_kind!==identity.runtimeKind)) return null;
        if (!current.runtime_run_id && current.state!=='queued') return null;
        const result = await tx.query<Invocation>(`UPDATE ai_invocations SET runtime_kind=$2,runtime_run_id=$3,runtime_heartbeat_at=clock_timestamp(),
          state=CASE WHEN state='queued' THEN 'running' ELSE state END,updated_at=now()
          WHERE id=$1 AND (expires_at IS NULL OR expires_at>clock_timestamp()) RETURNING ${columns}`, [identity.invocationId,identity.runtimeKind,identity.runtimeRunId]);
        return result.rows[0] ?? null;
      }, { mode: 'service' });
    },
  };
}
