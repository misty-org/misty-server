import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { PasswordHasher } from "../auth/passwords.js";
import type { createCollaborationTickets } from "../collaboration/tickets.js";
import type { ObjectStore } from "../storage/object-store.js";
import { clientInteger } from "../spaces/model.js";
import { AccountUnavailable } from "./repository.js";
import { AccountExportUnavailable, createExportFile, type ExportFile } from "./export-file.js";
import { exportQueries, exportContentLocks } from "./export-queries.js";
import { createExportReader } from "./export-reader.js";

import { AccountReauthenticationFailed, verifyAccountPassword } from "./reauthentication.js";
type Account = { id: string; name: string; username: string; email: string; avatar_version: string; created_at: Date; password_hash: string;
  email_updates_enabled: boolean; analytics_enabled: boolean; error_reporting_enabled: boolean };
async function account(tx: PoolClient, userId: string, sessionHash: string) {
  const row = (await tx.query<Account>(`SELECT u.id,u.name,u.username,u.email,u.avatar_version,u.created_at,u.password_hash,
    u.email_updates_enabled,u.analytics_enabled,u.error_reporting_enabled FROM users u JOIN sessions s ON s.user_id=u.id
    WHERE u.id=$1 AND u.lifecycle_state='active' AND s.token_hash=$2 AND s.expires_at>clock_timestamp() FOR SHARE OF u,s`, [userId, sessionHash])).rows[0];
  if (!row) throw new AccountUnavailable(); return row;
}
export function createAccountExport(options: { pool: Pool; passwords: PasswordHasher; tickets: ReturnType<typeof createCollaborationTickets> | null; store: ObjectStore | null }) {
  let active = 0;
  return {
    async manifest(userId: string, sessionHash: string, password: string, requestSignal: AbortSignal) {
      if (active >= 2) throw new AccountExportUnavailable();
      active++; let released = false, generationComplete = false, fileClosed = false;
      const release = () => { if (generationComplete && fileClosed && !released) { released = true; active--; } };
      const closed = () => { fileClosed = true; release(); };
      const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(60000)]);
      let file: ExportFile | undefined;
      try {
        signal.throwIfAborted();
        const before = await withTransaction(options.pool, async (tx) => {
          await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
          return account(tx, userId, sessionHash);
        }, { mode: "service" });
        await verifyAccountPassword(options.passwords, password, before.password_hash, signal);
        signal.throwIfAborted();
        file = await createExportFile({ signal: requestSignal, onClose: closed });
        const output = file;
        await withTransaction(options.pool, async (tx) => {
          await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
          const read = createExportReader(tx, signal);
          // Acquire all Spaces before the account, as usage and ownership do.
          await read(`SELECT s.id FROM spaces s WHERE s.lifecycle_state='active' AND EXISTS(SELECT 1 FROM space_members m
            WHERE m.space_id=s.id AND m.user_id=$1) ORDER BY s.id FOR SHARE OF s`, [userId], async () => {});
          const current = await account(tx, userId, sessionHash);
          if (current.password_hash !== before.password_hash) throw new AccountReauthenticationFailed();
          for (const query of exportContentLocks) await read(query, [userId], async () => {});
          const timestamp = (await tx.query<{ now: Date }>("SELECT transaction_timestamp() AS now")).rows[0]!.now;
          const profile = { id: current.id, name: current.name, username: current.username, email: current.email, avatar_version: clientInteger(current.avatar_version), created_at: current.created_at };
          const settings = { email_updates_enabled: current.email_updates_enabled, analytics_enabled: current.analytics_enabled, error_reporting_enabled: current.error_reporting_enabled };
          await output.write(`{"account_data":{"format_version":2,"exported_at":${JSON.stringify(timestamp)},"account":${JSON.stringify(profile)},"settings":${JSON.stringify(settings)}`);
          async function array(name: string, query: string, consume: (raw: string) => Promise<void> = raw => output.write(raw), values = [userId], omitAgentFields = false) {
            await output.write(`,${JSON.stringify(name)}:[`); let first = true;
            await read(query, values, async raw => { if (!first) await output.write(","); first = false; await consume(raw); }, omitAgentFields);
            await output.write("]");
          }
          await array("spaces", exportQueries.spaces);
          await output.write(',"journal":null,"assets":null');
          await array("authored_messages", exportQueries.messages);
          await array("agents", exportQueries.agents, async raw => {
            const id = (JSON.parse(raw) as { id: string }).id;
            await output.write(raw.slice(0, -1));
            await array("versions", exportQueries.versions, undefined, [id], true);
            // Preserve format v2: Go exports owned definitions and
            // versions, with an empty memberships array, rather than runtime logs.
            await output.write(',"space_memberships":[]}');
          }, [userId], true);
          await array("cloud_connections", exportQueries.connections);
          await output.write("}");
          await array("documents", exportQueries.documents, async raw => {
            if (!options.tickets) throw new AccountExportUnavailable("collaboration_unavailable");
            const document = JSON.parse(raw) as { kind: "note" | "drawing"; id: string; space_id: string; acl_version: number };
            const ticket = await options.tickets({ userId, spaceId: document.space_id, kind: document.kind, id: document.id, role: "viewer", aclVersion: clientInteger(document.acl_version) }, true);
            const url = new URL(ticket.url.replace(/^ws/, "http")); url.searchParams.set("export", "1"); url.searchParams.set("ticket", ticket.ticket);
            await output.write(`${raw.slice(0, -1)},"download_url":${JSON.stringify(url.href)},"expires_at":${JSON.stringify(ticket.expires_at)}}`);
          });
          await array("assets", exportQueries.assets, async raw => {
            if (!options.store) throw new AccountExportUnavailable("account_export_assets_unavailable");
            const { object_key, ...asset } = JSON.parse(raw) as { object_key: string; filename: string; byte_size: number; mime_type: string; sha256: string };
            asset.byte_size = clientInteger(asset.byte_size);
            const download = await options.store.signDownload(object_key, asset.filename, new Date(Date.now() + 900000));
            await output.write(JSON.stringify({ ...asset, download: { ...download, mime_type: asset.mime_type, byte_size: asset.byte_size, sha256: asset.sha256 } }));
          });
          await output.write("}"); signal.throwIfAborted(); await account(tx, userId, sessionHash); signal.throwIfAborted();
        }, { mode: "service" }, { isolationLevel: "repeatable read" });
        signal.throwIfAborted(); generationComplete = true; return file.response();
      } catch (error) {
        // Cancellation can close the file while SQL is still unwinding. Retain
        // admission until both the transaction and the descriptor have finished.
        generationComplete = true;
        if (file) await file.close(); else closed();
        release();
        if (signal.aborted || error && typeof error === "object" && "code" in error && ["55P03", "57014", "40P01", "40001", "ENOSPC", "EDQUOT", "EMFILE", "ENFILE"].includes(String(error.code))) throw new AccountExportUnavailable();
        throw error;
      }
    },
  };
}
