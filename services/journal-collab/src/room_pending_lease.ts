import type * as Y from "yjs";

const LEASE_MS = 15_000;

interface PendingLease {
  version: number;
  connectionID: string;
  expiresAt: number;
}

export class PendingMarkdownLease {
  private lease: PendingLease | null = null;

  answer(
    document: Y.Doc,
    connectionID: string,
    readOnly: boolean,
    message: string,
    openConnection: (id: string) => boolean,
    now = Date.now(),
  ): string | null {
    let claim: { type?: unknown; version?: unknown };
    try {
      claim = JSON.parse(message) as typeof claim;
    } catch {
      return null;
    }
    if (claim.type !== "claim_pending_markdown" || typeof claim.version !== "number") return null;
    const version = claim.version;
    const metadata = document.getMap<unknown>("misty:document");
    const pending = Number(metadata.get("pending_version") ?? 0);
    const applied = Number(metadata.get("applied_version") ?? 0);
    let granted = false;
    if (!readOnly && version === pending && pending > applied) {
      const held = this.lease;
      const free =
        !held ||
        held.version !== version ||
        held.connectionID === connectionID ||
        held.expiresAt <= now ||
        !openConnection(held.connectionID);
      if (free) {
        this.lease = { version, connectionID, expiresAt: now + LEASE_MS };
        granted = true;
      }
    }
    return JSON.stringify({ type: "pending_markdown_lease", version, granted });
  }

  release(connectionID: string): void {
    if (this.lease?.connectionID === connectionID) this.lease = null;
  }
}
