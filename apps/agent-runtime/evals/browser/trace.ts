// A waterfall for one local agent run: where its time went between model
// turns, tool calls, the MCP hop and the device job hop. Reads the local
// development database (as the acceptance harness does) and the runtime's
// tool_timing log lines.
//
//   npm run trace -- invocation_123
//
// MISTY_TRACE_DB_CONTAINER, MISTY_TRACE_DB_USER, MISTY_TRACE_DB_NAME and
// MISTY_TRACE_RUNTIME_CONTAINER override the Compose defaults.
import { execFileSync } from "node:child_process";

const runId = process.argv[2] ?? "";
if (!/^[A-Za-z0-9_-]{6,120}$/.test(runId)) {
  console.error("Usage: npm run trace -- <invocation or run id>");
  process.exit(2);
}

const container = process.env.MISTY_TRACE_DB_CONTAINER ?? "misty-server-postgres-1";
type SqlRow = Record<string, string | undefined>;
const sql = (query: string): SqlRow[] => {
  const out = execFileSync("docker", ["exec", container, "psql", "-U", process.env.MISTY_TRACE_DB_USER ?? "misty", "-d", process.env.MISTY_TRACE_DB_NAME ?? "misty_server",
    "-v", "ON_ERROR_STOP=1", "-A", "-t", "-c", `SELECT coalesce(json_agg(t),'[]') FROM (${query}) t`], { encoding: "utf8" });
  return JSON.parse(out.trim() || "[]");
};
// The id was checked against a strict pattern above; it is safe to inline.
const turns = sql(`SELECT node_id, created_at FROM agent_model_turn_claims WHERE run_id='${runId}' ORDER BY created_at`);
const events = sql(`SELECT event_type, created_at, payload->>'toolCallId' AS call, payload->>'toolName' AS tool FROM ai_invocation_events WHERE invocation_id='${runId}' AND event_type IN ('tool.started','tool.completed','tool.failed','invocation.completed','invocation.failed','invocation.canceled') ORDER BY sequence`);
const jobs = sql(`SELECT node_id, operation, state, created_at, last_heartbeat_at, execution_started_at, completed_at FROM workflow_device_node_jobs WHERE invocation_id='${runId}' OR run_id='${runId}' ORDER BY created_at`);
const timings = new Map<string, { token_ms: number; mcp_ms: number }>();
try {
  const logs = execFileSync("docker", ["logs", process.env.MISTY_TRACE_RUNTIME_CONTAINER ?? "misty-server-agent-runtime-1"], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], maxBuffer: 64 << 20 });
  for (const line of logs.split("\n")) {
    const match = /tool_timing (\{.*\})/.exec(line);
    if (!match?.[1]) continue;
    const entry = JSON.parse(match[1]) as { run: string; call: string; token_ms: number; mcp_ms: number };
    if (entry.run === runId) timings.set(entry.call, entry);
  }
} catch {
  // Without runtime logs the waterfall still shows model, tool and device time.
}

const at = (value?: string) => (value ? Date.parse(value) : NaN);
const all = [...turns.map((turn) => at(turn.created_at)), ...events.map((event) => at(event.created_at)), ...jobs.map((job) => at(job.created_at))].filter(Number.isFinite);
if (!all.length) {
  console.error(`No trace for ${runId} in ${container}.`);
  process.exit(1);
}
const origin = Math.min(...all);
const seconds = (ms: number) => `${(ms / 1000).toFixed(2)}s`;

type Row = { time: number; text: string };
const rows: Row[] = turns.map((turn) => ({ time: at(turn.created_at), text: `model turn ${turn.node_id}` }));
const started = new Map<string, number>();
let toolMs = 0;
for (const event of events) {
  const time = at(event.created_at);
  const type = event.event_type ?? "";
  if (type === "tool.started" && event.call) started.set(event.call, time);
  else if (event.call && started.has(event.call)) {
    const duration = time - started.get(event.call)!;
    toolMs += duration;
    const timing = timings.get(event.call);
    rows.push({ time: started.get(event.call)!, text: `tool ${event.tool} ${type.slice(5)} in ${seconds(duration)}${timing ? ` (token ${timing.token_ms}ms, MCP ${timing.mcp_ms}ms)` : ""}` });
  } else if (type.startsWith("invocation.")) rows.push({ time, text: type });
}
let hopMs = 0;
for (const job of jobs) {
  const queued = at(job.created_at);
  const begun = at(job.execution_started_at);
  const done = at(job.completed_at);
  // Short jobs are never re-heartbeated, so the heartbeat is the claim time.
  const claimed = at(job.last_heartbeat_at);
  if (Number.isFinite(begun)) hopMs += begun - queued;
  rows.push({ time: queued, text: `  device ${job.operation} ${job.state}: queued→claimed ${Number.isFinite(claimed) && claimed <= done ? seconds(claimed - queued) : "?"}, →started ${Number.isFinite(begun) ? seconds(begun - queued) : "?"}, ran ${Number.isFinite(begun) && Number.isFinite(done) ? seconds(done - begun) : "?"}` });
}
rows.sort((a, b) => a.time - b.time);
for (const row of rows) console.log(`${seconds(row.time - origin).padStart(8)}  ${row.text}`);
const total = Math.max(...all) - origin;
console.log(`\nTotal ${seconds(total)} · in tools ${seconds(toolMs)} (${Math.round((toolMs / total) * 100)}%) · device hop before execution ${seconds(hopMs)} · model turns ${turns.length}`);
