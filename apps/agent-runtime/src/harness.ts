/** Misty-owned lifecycle boundary. Engine continuation tokens stay in the adapter. */
export const MISTY_HARNESS_VERSION = "vercel-workflow/1" as const;
export interface HarnessStart {
  mistyRunId: string;
  controlPlaneURL: string;
  adapterVersion: typeof MISTY_HARNESS_VERSION;
}
export type HarnessResume =
  | { kind: "approval"; token: string; approvalId: string; approved: boolean }
  | { kind: "device"; token: string; available: boolean };
export interface MistyHarness {
  readonly version: typeof MISTY_HARNESS_VERSION;
  start(input: HarnessStart): Promise<{ runtimeRunId: string }>;
  status(runtimeRunId: string): Promise<string>;
  resume(input: HarnessResume): Promise<void>;
  cancel(runtimeRunId: string): Promise<void>;
}
export interface HarnessCheckpoint {
  node_id: string;
  state: "running" | "completed" | "failed";
  phase: string;
  progress: number;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  error_message?: string;
}
export interface HarnessCompletion {
  status: "success" | "incomplete" | "failed";
  text: string;
  usage?: Record<string, unknown>;
  error_code?: string;
  error_message?: string;
}
/** All effects and checkpoints are delegated to the authoritative control plane. */
export interface HarnessExecution {
  executeCapability(callId: string, name: string, input: unknown): Promise<unknown>;
  checkpoint(event: HarnessCheckpoint): Promise<void>;
  complete(result: HarnessCompletion): Promise<void>;
}
