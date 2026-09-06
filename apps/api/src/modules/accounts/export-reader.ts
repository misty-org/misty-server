import type { PoolClient } from "pg";
import { AccountExportUnavailable } from "./export-file.js";

/** A transaction cursor sends one bounded row at a time. Keeping raw JSON for
 * authored content also preserves PostgreSQL numbers beyond JS safe integers.
 */
export function createExportReader(tx: PoolClient, signal: AbortSignal) {
  let sequence = 0;
  return async (query: string, values: unknown[], consume: (raw: string) => Promise<void>, omitAgentFields = false) => {
    const cursor = `account_export_${++sequence}`;
    const json = omitAgentFields
      ? `(to_jsonb(r)-CASE WHEN r.model_id='' THEN 'model_id' ELSE '' END-CASE WHEN r.reasoning_effort='' THEN 'reasoning_effort' ELSE '' END
        -CASE WHEN to_jsonb(r)->'deleted_at'='null'::jsonb THEN 'deleted_at' ELSE '' END)::text`
      : "row_to_json(r)::text";
    signal.throwIfAborted();
    await tx.query(`DECLARE ${cursor} NO SCROLL CURSOR FOR SELECT CASE WHEN octet_length(${json})<=16777216 THEN ${json} ELSE NULL END AS data FROM (${query}) r`, values);
    let failed = false;
    try {
      for (;;) {
        signal.throwIfAborted();
        const row = (await tx.query<{ data: string | null }>(`FETCH FORWARD 1 FROM ${cursor}`)).rows[0];
        if (!row) break;
        if (row.data === null) throw new AccountExportUnavailable("account_export_too_large");
        await consume(row.data);
      }
    } catch (error) { failed = true; throw error; }
    finally {
      // A timed-out FETCH aborts the transaction. CLOSE then reports 25P02;
      // preserve the original contention/deadline error for the HTTP boundary.
      if (failed) await tx.query(`CLOSE ${cursor}`).catch(() => {});
      else await tx.query(`CLOSE ${cursor}`);
    }
  };
}
