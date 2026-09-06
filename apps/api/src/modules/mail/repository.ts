import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../spaces/access.js";
import { requireConnectionActor } from "../connections/access.js";
import { createMailAudit } from "./audit.js";
import { withDraftConnection } from "./draft-coordination.js";

export type MailConnection = { id: string; provider: string; account_id: string; account_display: string; status: string; last_error_code: string };
export function createMailRepository(pool: Pool) {
  return {
    audit: createMailAudit(pool),
    draftConnection: <T>(connectionId: string, draftId: string, signal: AbortSignal, operation: (pool: Pick<Pool, "connect">, signal: AbortSignal) => Promise<T>) => withDraftConnection(pool, connectionId, draftId, signal, operation),
    list: (actor: SpaceActor) => withTransaction(pool, async (tx) => {
      await requireConnectionActor(tx, actor, "mail.read");
      return (await tx.query<MailConnection>(`SELECT id,provider,account_id,account_display,status,last_error_code FROM connected_accounts
        WHERE user_id=$1 AND revoked_at IS NULL AND provider IN ('google','microsoft') AND capabilities ? 'mail'
        ORDER BY provider,account_display,id`, [actor.userId])).rows;
    }, { mode: "service" }),
    assertReader: (actor: SpaceActor) => withTransaction(pool, (tx) => requireConnectionActor(tx, actor, "mail.read"), { mode: "service" }),
  };
}
