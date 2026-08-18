export interface SpaceTaskContext {
  run_id: string;
  agent_id: string;
  space_id: string;
  model_id: string;
  reasoning_effort?: "low" | "medium" | "high" | "";
  system: string;
  prompt: string;
  task: {
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
