import type { RuntimeIdentity } from "../src/control-plane.js";
import type { DocumentIds } from "../src/document-ids.js";
import type { HarnessExecution } from "../src/harness.js";
import type { Ledger } from "../src/ledger.js";
import type { serialToolLifecycle } from "../src/serial-tool-lifecycle.js";
import type { SpaceTaskContext } from "../src/types.js";
import type { RunTools } from "./run-tools.js";

/** What one durable agent run shares between its loop, its tools and its finish checks. */
export interface AgentRun {
  identity: RuntimeIdentity;
  context: SpaceTaskContext;
  execution: HarnessExecution;
  order: ReturnType<typeof serialToolLifecycle>;
  // Calls Misty reported as rejected without effect; the model may correct them.
  rejected: Set<string>;
  // Each tab's latest documentId, so a garbled copy in an action is repaired (src/document-ids.ts).
  documents: DocumentIds;
  // The parts this run was asked for, with their evidence; a resume starts from the earlier run's rows.
  ledger: Ledger;
  limit: number;
  modelTurn: number;
  // Recomputed from the transcript before every model call.
  visibleTools: string[];
  // The agent swallows errors thrown from its step hooks, so a failed
  // checkpoint is held here and rethrown once the call returns.
  hookFailure: unknown;
  // A finish is verified once (src/finish-verification.ts); the next one stands.
  verified: boolean;
  // Give-ups sent back after that turn, and what the run had found at the last one.
  persisted: number;
  persistedAt?: Set<string>;
  // Answers sent back for reporting the work instead of giving it (src/finish-verification.ts).
  reportedOnly: number;
  /** The run's current tools and agent; a screen that joins mid-run rebuilds them. */
  tools: RunTools;
}

/** Declines a call the serial order already gave up on. */
export function guard(run: AgentRun, callId: string) {
  const declined = run.order.declined(callId);
  if (declined) throw new Error(declined);
}
