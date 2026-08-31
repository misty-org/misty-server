export interface SpaceTaskContext {
  run_id: string;
  agent_id: string;
  space_id: string;
  space_name: string;
  space_kind: string;
  timezone: string;
  current_time: string;
  members: Array<{ user_id: string; name: string; role: string }>;
  model_id: string;
  reasoning_effort?: "low" | "medium" | "high" | "";
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
    mime_type: "image/jpeg" | "image/png" | "image/webp";
    data_url: string;
    width: number;
    height: number;
    content_hash: string;
  }>;
  file_warnings: string;
  allowed_tools: string[];
  /**
   * Write tools the control plane derived from the user's explicit request.
   * A run must not report success until each one has a confirmed tool result.
   */
  required_tools?: string[];
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
}
