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
  file_warnings: string;
  allowed_tools: string[];
}

export interface RuntimeToolContext {
  mistyRunId: string;
  runtimeRunId: string;
  controlPlaneURL: string;
}
