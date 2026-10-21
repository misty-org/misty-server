// Records one run as Odysseys' judge reads a trajectory: a line per step (the action and
// what came back) and screenshots of the agent's own browser tabs, captured through the
// desktop's debug bridge right after each browser step and once more at the end, since
// tasks ask for pages left open "as visual proof".
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

export interface Step {
  step: number;
  action: string;
  response: string;
  screenshots: string[];
}

interface Bridge {
  url: string;
  token: string;
}

const agentPage = (label: string) => label.startsWith("misty-browser-agent-");
// Tools whose step changes or shows a page, and so earn a picture.
const visual = (tool: string) => tool.startsWith("browser.") || tool === "screen.open";

export class Recorder {
  readonly steps: Step[] = [];
  private shots = 0;
  /** Each running call's step, so parallel calls keep their own results. */
  private calls = new Map<string, Step>();
  /** The last picture of each page, so an unchanged page is not pictured again. */
  private seen = new Map<string, string>();
  /** Pages already open when the run started belong to the person, not the run. */
  private before: Set<string> | undefined;
  /** The run this recorder pictures, from the client's eval.created event. */
  private runId = "";

  constructor(
    private readonly dir: string,
    private readonly bridge: Bridge | undefined,
  ) {
    mkdirSync(dir, { recursive: true });
  }

  /**
   * Starts from an earlier run of the same task (a checkpoint, odysseys.eval.ts): its steps and
   * pictures come first, so the judge reads one trajectory. Its final answer becomes an earlier
   * answer, since the resumed run writes the final one.
   */
  carry(fromDir: string): void {
    const file = join(fromDir, "steps.jsonl");
    if (!existsSync(file)) return;
    for (const line of readFileSync(file, "utf8").split("\n").filter(Boolean)) {
      const earlier = JSON.parse(line) as { action: string; response: string; screenshots?: string[] };
      const screenshots = (earlier.screenshots ?? []).flatMap((shot) => {
        if (!existsSync(join(fromDir, shot))) return [];
        const copy = `shot-${String(++this.shots).padStart(3, "0")}${shot.slice(shot.lastIndexOf("."))}`;
        copyFileSync(join(fromDir, shot), join(this.dir, copy));
        return [copy];
      });
      this.steps.push({
        step: this.steps.length + 1,
        action: earlier.action === "final_answer" ? "earlier_answer" : earlier.action,
        response: earlier.response,
        screenshots,
      });
    }
  }

  /** Notes the pages open before the run, so only the run's own are pictured. */
  async start(): Promise<void> {
    if (!this.bridge) return;
    const pages = await this.call<{ pages: Array<{ label: string }> }>("/pages", {}).catch(() => ({
      pages: [],
    }));
    this.before = new Set(pages.pages.map((page) => page.label));
  }

  /** Feeds one run event; captures pages after a browser step finishes. */
  async observe(event: Record<string, unknown>): Promise<void> {
    if (event.type === "eval.created") {
      this.runId = String(event.invocationId ?? "");
      return;
    }
    const tool = String(event.toolName ?? "");
    const id = String(event.toolCallId ?? "");
    const stepOf = () =>
      this.calls.get(id) ??
      [...this.steps]
        .reverse()
        .find((step) => step.action === tool.replace(/\./g, "_") && !step.response);
    if (event.type === "tool.started") {
      const step = {
        step: this.steps.length + 1,
        action: tool.replace(/\./g, "_"),
        response: "",
        screenshots: [],
      };
      this.steps.push(step);
      if (id) this.calls.set(id, step);
    }
    if (event.type === "tool.detail" && typeof event.result === "string") {
      const step = stepOf();
      if (step) step.response = summarize(event.result);
    }
    if (event.type === "tool.completed" || event.type === "tool.failed") {
      const step = stepOf();
      if (event.type === "tool.failed" && step)
        step.response ||= `failed: ${String(event.error ?? "")}`.slice(0, 300);
      if (step && visual(tool)) step.screenshots.push(...(await this.capture()));
    }
  }

  /** The final answer and a last picture of every page the agent left open. */
  async finish(answer: string): Promise<void> {
    // Every open page is pictured again, changed or not: the judge sees the last pictures, and
    // a tab opened early and left as proof would otherwise only appear among the dropped ones.
    this.steps.push({
      step: this.steps.length + 1,
      action: "final_answer",
      response: answer.slice(0, 40_000),
      screenshots: await this.capture(true),
    });
    const lines = this.steps.map((step) =>
      JSON.stringify({
        step: step.step,
        action: step.action,
        response: step.response,
        screenshot: step.screenshots[0] ?? "",
        screenshots: step.screenshots,
      }),
    );
    writeFileSync(join(this.dir, "steps.jsonl"), lines.join("\n") + "\n");
  }

  /** Every screenshot in order, as files in the run's folder. */
  get screenshots(): string[] {
    return this.steps.flatMap((step) => step.screenshots.map((file) => join(this.dir, file)));
  }

  private async capture(all = false): Promise<string[]> {
    if (!this.bridge) return [];
    const pages = await this.call<{
      pages: Array<{ label: string; url: string; runId?: string | null }>;
    }>("/pages", {}).catch(() => ({ pages: [] }));
    const files: string[] = [];
    // Pages opened during the run: its own tabs and tabs it opened for the person. Every run
    // gets new tabs, so an agent tab open before it started is an earlier task's, kept for
    // the person until it idles out, and would crowd this run's evidence out of the judge's view.
    // Several tasks run at once: a page in an agent's window is this run's only when that window
    // works for it; one in the person's window goes by when it opened.
    const ours = (page: { label: string; runId?: string | null }) =>
      page.runId
        ? page.runId === this.runId
        : this.before
          ? !this.before.has(page.label)
          : agentPage(page.label);
    for (const page of pages.pages.filter((item) => ours(item) && item.url.startsWith("http"))) {
      // 640 points is about 1280 pixels on a Retina display: legible text at a judge's price.
      const image = await this.call<{ dataUrl?: string }>("/capture", {
        label: page.label,
        maxDimension: 640,
      }).catch(() => undefined);
      const match = /^data:image\/(\w+);base64,(.+)$/.exec(String(image?.dataUrl ?? ""));
      if (!match) continue;
      const digest = createHash("sha1").update(match[2]!).digest("hex");
      if (!all && this.seen.get(page.label) === digest) continue;
      this.seen.set(page.label, digest);
      const file = `shot-${String(++this.shots).padStart(3, "0")}.${match[1] === "png" ? "png" : "jpg"}`;
      writeFileSync(join(this.dir, file), Buffer.from(match[2]!, "base64"));
      files.push(file);
    }
    return files;
  }

  private async call<T>(path: string, body: unknown): Promise<T> {
    const response = await fetch(`${this.bridge!.url}${path}`, {
      method: "POST",
      headers: {
        authorization: `Bearer ${this.bridge!.token}`,
        "content-type": "application/json",
      },
      body: JSON.stringify(body),
    });
    const reply = (await response.json()) as { ok: boolean; result?: T; error?: string };
    if (!reply.ok) throw new Error(reply.error ?? String(response.status));
    return reply.result as T;
  }
}

/** A tool result as one short line for the action history: what page, what it found. */
function summarize(result: string): string {
  try {
    const value = JSON.parse(result) as Record<string, unknown>;
    const parts = [
      value.url,
      value.title,
      value.status,
      value.message,
      value.text,
      (value.snapshot as { tree?: unknown } | undefined)?.tree,
      value.tree,
    ]
      .filter((part) => typeof part === "string" || typeof part === "number")
      .map(String);
    return (parts.join(" | ") || result).replace(/\s+/g, " ").slice(0, 600);
  } catch {
    return result.replace(/\s+/g, " ").slice(0, 600);
  }
}
