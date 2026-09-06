import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { ControlSender } from "./control.js";

type Command = { id: string; resource_id: string; command: string; payload: unknown; attempts: number };
export function createControlJobs(pool: Pool, send: ControlSender) {
  return {
    async runOnce() {
      let work = 0;
      for (const kind of ["note", "drawing"] as const) {
        const table = `space_${kind}_control_outbox`;
        // Content replacement needs ordering and replay protection in both room
        // runtimes before its native ownership switch. Leave those jobs intact.
        const commands = await withTransaction(pool, async (client) => (await client.query<Command>(`WITH candidates AS (
          SELECT o.id FROM ${table} o WHERE delivered_at IS NULL AND next_attempt_at<=now() AND o.command IN ('acl','disconnect','purge','bootstrap')
          AND (o.command<>'purge' OR NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind='${kind}' AND h.target_id=o.${kind}_id))
          ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 4)
          UPDATE ${table} o SET attempts=attempts+1,next_attempt_at=now()+interval '2 minutes' FROM candidates c WHERE o.id=c.id
          RETURNING o.id,o.${kind}_id AS resource_id,o.command,o.payload,o.attempts`)).rows, { mode: "service" });
        await Promise.all(commands.map(async (command) => {
          let delivered = false;
          try { await send(kind, command.resource_id, command.command, command.payload); delivered = true; } catch { /* Keep failures durable, without storing response text or secrets. */ }
          await withTransaction(pool, async (client) => {
            // Attempts is the generation fence, including after a process crash
            // and reclaim. A late acknowledgement cannot complete a newer lease.
            if (delivered) await client.query(`UPDATE ${table} SET delivered_at=now(),last_error=''
              WHERE id=$1 AND attempts=$2 AND delivered_at IS NULL`, [command.id, command.attempts]);
            else await client.query(`UPDATE ${table} SET last_error='control_delivery_failed',next_attempt_at=now()+LEAST(3600,10*power(2,LEAST(attempts,8))) * interval '1 second'
              WHERE id=$1 AND attempts=$2 AND delivered_at IS NULL`, [command.id, command.attempts]);
          }, { mode: "service" });
        }));
        work += commands.length;
      }
      return work > 0;
    },
  };
}
