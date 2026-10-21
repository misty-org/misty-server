import { createHash } from "node:crypto";

// The progress watch (docs/plans/odysseys-campaign.md). Odysseys tasks are long on purpose, so no
// clock ends a run. Every few minutes the eval looks at what the run did since its last look:
// - progressing: a result it had not produced before (a new page, new data, a changed page);
// - looping: tool calls whose results all repeat earlier ones, for `loopChecks` looks in a row;
// - stalled: no tool call, result or answer text at all, for `stallChecks` looks in a row.
// Only a looping or stalled run is stopped, and it is still graded.

/** A result without what changes on every read of the same page: ids, refs and times. */
function digest(result: string): string {
  const stable = result
    .replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, "")
    .replace(/\bref=e\d+\b|\[ref=e\d+\]/g, "")
    .replace(/\d{4}-\d{2}-\d{2}T[\d:.]+Z?/g, "")
    .replace(/"(documentId|waitedMs|durationMs|elapsedMs)":\s*("[^"]*"|\d+)/g, "");
  return createHash("sha1").update(stable).digest("hex");
}

export class ProgressWatch {
  private readonly seen = new Set<string>();
  private readonly started = Date.now();
  private calls = 0;
  private fresh = 0;
  private activity = 0;
  private idle = 0;
  private repeating = 0;

  constructor(
    private readonly label: string,
    private readonly stallChecks = 2,
    private readonly loopChecks = 2,
  ) {}

  observe(event: Record<string, unknown>): void {
    if (event.type === "tool.started") {
      this.calls++;
      this.activity++;
    }
    if (event.type === "response.delta" || event.type === "assistant.message") this.activity++;
    if (event.type === "tool.detail" && typeof event.result === "string") {
      this.activity++;
      const key = `${String(event.toolName)}:${digest(event.result)}`;
      if (!this.seen.has(key)) {
        this.seen.add(key);
        this.fresh++;
      }
    }
  }

  /** One look: the reason to stop the run, or undefined while it makes progress. */
  check(): string | undefined {
    const { calls, fresh, activity } = this;
    this.calls = this.fresh = this.activity = 0;
    this.idle = activity === 0 ? this.idle + 1 : 0;
    this.repeating = calls > 0 && fresh === 0 ? this.repeating + 1 : 0;
    const minutes = Math.round((Date.now() - this.started) / 60_000);
    const state = this.idle ? "quiet" : this.repeating ? "repeating" : "progressing";
    process.stderr.write(`WATCH ${this.label} ${minutes}m: ${calls} calls, ${fresh} new results — ${state}\n`);
    if (this.idle >= this.stallChecks) return `stalled: no activity in ${this.idle} checks`;
    if (this.repeating >= this.loopChecks) return `looping: only repeated results in ${this.repeating} checks`;
    return undefined;
  }
}
