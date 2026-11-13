import type * as Y from "yjs";

/** How long a granted editor has to apply the markdown before another may take it over. */
const LEASE_MS = 15_000;

interface PendingLease {
  version: number;
  connectionID: string;
  expiresAt: number;
}

/**
 * Picks the one open copy that applies a note's pending markdown to its editor content.
 *
 * The server writes markdown (an agent's or the API's rewrite) beside the editor content, and an
 * editor's app converts it, since the editor's schema lives in the app. Two copies converting the
 * same version would both write the body and double it, so each editor asks the room, which is the
 * only serialized view of the doc, and the room grants a version to one connection at a time.
 */
export class PendingMarkdownLease {
  private lease: PendingLease | null = null;

  /** Answers a `claim_pending_markdown` message, or returns null for any other message. */
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

  /** Frees the lease when its holder disconnects, so the next claim gets it at once. */
  release(connectionID: string): void {
    if (this.lease?.connectionID === connectionID) this.lease = null;
  }
}
