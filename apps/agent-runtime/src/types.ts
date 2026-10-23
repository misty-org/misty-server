export interface SpaceTaskContext {
  /** A workflow's saved browser steps, replayed before any model call. */
  browser_replay?: unknown;
  model_id: string;
  reasoning_effort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "";
  system: string;
  prompt: string;
  /** A Teach-mode lesson turn: run the tutor's fast lane (src/teach-turn.ts), not the agent loop. */
  teach?: boolean;
  /** Lookups a lesson turn may make: more when planning a lesson, one on a step. */
  teach_tool_budget?: number;
  display_captures?: Array<NonNullable<SpaceTaskContext["capture"]> & { screen: string; primary: boolean; captured_at?: number; display_id?: number; source?: string; controls?: ScreenControl[] }>;
  capture?: {
    id: string;
    name: string;
    mime_type: "image/jpeg" | "image/png" | "image/webp";
    data_url: string;
    width: number;
    height: number;
    content_hash: string;
  };
  attachments?: Array<{
    id: string;
    name: string;
    mime_type: "image/jpeg" | "image/png" | "image/webp" | "application/pdf" | "text/plain";
    data_url: string;
    width: number;
    height: number;
    content_hash: string;
  }>;
  model_turn_limit?: number;
  /** The run model's context window in tokens, from the Gateway catalog. */
  context_window_tokens?: number;
  allowed_tools: string[];
}

export interface RuntimeToolContext {
  mistyRunId: string;
  runtimeRunId: string;
  controlPlaneURL: string;
}

export interface MCPRunAccess {
  access_token: string;
  token_type: "Bearer";
  expires_in: number;
  mcp_path: string;
  protocol: "2026-07-28";
}

export interface MCPRemoteTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  /** Read-only and run on the server or a provider, never a device: it holds
   * no durable wait, so it may run alongside other such reads. */
  concurrent?: boolean;
  /** Misty's read-only annotation; a failed read is always safe to retry. */
  readOnly?: boolean;
}

/** One of Misty's own visible controls, in its capture's pixels; x and y are its center. */
export interface ScreenControl {
  label: string;
  role: string;
  x: number;
  y: number;
  width: number;
  height: number;
  /** "hover" when the control is invisible until the pointer is over it. */
  reveal?: "hover";
}
