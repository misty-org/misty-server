export interface SpaceTaskContext {
 /** Only retained to reject stale routine submissions. */
 routine_execution?: unknown;
  run_id: string;
  agent_id: string;
  space_id: string;
  space_name: string;
  space_kind: string;
  timezone: string;
  current_time: string;
  members: Array<{ user_id: string; name: string; role: string }>;
  model_id: string;
  vision_model_id?: string;
  reasoning_effort?: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "";
  run_mode: "ask" | "auto" | "full";
  system: string;
  prompt: string;
  task?: {
    id: string;
    task_key: string;
    title: string;
    notes: string;
    status: string;
  };
  attached_sources: unknown[];
  companion_mode?: "team" | "auto";
  companion_explanation?: boolean;
  display_captures?: Array<NonNullable<SpaceTaskContext["capture"]> & { screen: string; primary: boolean; captured_at?: number; display_id?: number; source?: string }>;
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
  file_warnings: string;
  model_turn_limit?: number;
  /** The run model's context window in tokens, from the Gateway catalog. */
  context_window_tokens?: number;
  allowed_tools: string[];
  managed_misty?: boolean;
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
  /** Misty's read-only annotation; a failed read is always safe to retry. */
  readOnly?: boolean;
}
