// A small Misty API client for the workflow eval, against a local stack only.
// It signs in as the person running the eval, so runs land in their own
// account with their memory, spaces and connected apps, like any other run.
import { randomUUID } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { compact } from "./state.js";

export const api = (process.env.MISTY_ACCEPTANCE_API ?? "http://127.0.0.1:8081").replace(/\/$/, "");
const signInFile = process.env.MISTY_WORKFLOW_EVAL_SIGNIN ?? resolve(import.meta.dirname, "../../../../../.misty/acceptance/workflow-eval-signin.json");

function assertLocal() {
  const host = new URL(api).hostname;
  if (!["127.0.0.1", "localhost", "::1", "[::1]"].includes(host) && !host.endsWith(".localhost")) throw new Error(`The workflow eval runs only against a local stack, not ${host}.`);
}

export interface EvalRun {
  /** The invocation's id, for reading its usage from the billing ledger. */
  invocationId: string;
  terminal: string;
  answer: string;
  /** Tool calls in order, model-visible names. */
  tools: string[];
  screenRequests: string[];
  events: string[];
  /** What tools reported reading: titles and sites from tool.detail events. */
  pagesRead: string[];
  /** Each tool's raw result, when the local server sends them (MISTY_AI_TOOL_RESULT_EVENTS=1). */
  toolResults: string[];
}

/** A tool result without what a judge can't use (raw MIME, links, avatars), as compact JSON. */
function readable(result: string): string {
  try {
    return JSON.stringify(compact(JSON.parse(result)) ?? {});
  } catch {
    return result;
  }
}

export class EvalClient {
  private cookies = new Map<string, string>();

  /** Signs in with the email and password saved in the sign-in file (see README.md). */
  static async open(): Promise<EvalClient> {
    assertLocal();
    if (!existsSync(signInFile)) throw new Error(`No sign-in file at ${signInFile}; see README.md.`);
    const client = new EvalClient();
    await client.json("/login", { method: "POST", body: readFileSync(signInFile, "utf8") });
    return client;
  }

  private async send(path: string, init: RequestInit = {}) {
    const headers = new Headers(init.headers);
    if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    headers.set("X-Misty-CSRF", "1");
    if (this.cookies.size) headers.set("Cookie", [...this.cookies].map(([name, value]) => `${name}=${value}`).join("; "));
    const response = await fetch(`${api}${path}`, { ...init, headers });
    for (const cookie of response.headers.getSetCookie()) {
      const pair = cookie.split(";")[0] ?? "";
      const index = pair.indexOf("=");
      if (index > 0) this.cookies.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim());
    }
    return response;
  }

  /** One request, refreshing an expired session once. */
  async raw(path: string, init: RequestInit = {}) {
    const response = await this.send(path, init);
    if (response.status !== 401 || path === "/auth/refresh") return response;
    const refreshed = await this.send("/auth/refresh", { method: "POST" });
    return refreshed.ok ? this.send(path, init) : response;
  }

  async json<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await this.raw(path, init);
    const text = await response.text();
    if (!response.ok) throw new Error(`${init.method ?? "GET"} ${path}: ${response.status} ${text.slice(0, 300)}`);
    return (text ? JSON.parse(text) : undefined) as T;
  }

  /** Starts one run and reads its events until it ends. */
  /** `approve` names the app actions (Composio slugs) whose approval cards the run may pass, because the task asked for them. */
  /** `observe` sees every event as it arrives (the Odysseys recorder captures pages with it). */
  async run(body: Record<string, unknown>, timeoutMs = 10 * 60_000, approve: string[] = [], observe?: (event: Record<string, unknown>) => Promise<void>): Promise<EvalRun> {
    const key = `workflow-eval-${randomUUID()}`;
    const created = await this.json<{ eventsUrl: string }>("/ai/invocations", {
      method: "POST",
      headers: { "Idempotency-Key": key },
      body: JSON.stringify({ trigger: "message", context: [], execution_mode: "user", idempotency_key: key, timezone: "America/Los_Angeles", ...body }),
    });
    const approved = new Set<string>();
    const run: EvalRun = { terminal: "", answer: "", tools: [], screenRequests: [], events: [], pagesRead: [], toolResults: [], invocationId: created.eventsUrl.split("/").at(-2) ?? "" };
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const response = await this.raw(created.eventsUrl, { headers: { Accept: "text/event-stream" }, signal: controller.signal });
      if (!response.ok || !response.body) throw new Error(`events: ${response.status}`);
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffer += decoder.decode(chunk.value, { stream: true }).replace(/\r\n/g, "\n");
        for (let end = buffer.indexOf("\n\n"); end >= 0; end = buffer.indexOf("\n\n")) {
          const data = buffer.slice(0, end).split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trimStart()).join("\n");
          buffer = buffer.slice(end + 2);
          if (!data) continue;
          const event = JSON.parse(data) as { type: string; [key: string]: unknown };
          run.events.push(event.type);
          if (observe) await observe(event).catch(() => undefined);
          if (event.type === "response.delta") run.answer += String(event.delta ?? "");
          if (event.type === "assistant.message") run.answer = String(event.text ?? run.answer);
          // Started events carry catalog names (note.search); the model sees underscores.
          if (event.type === "tool.started") run.tools.push(String(event.toolName).replace(/\./g, "_"));
          if (event.type === "tool.detail") {
            const rows = (event.activity as { rows?: Array<{ title?: string; meta?: string }> } | undefined)?.rows ?? [];
            if (rows.length) run.pagesRead.push(`${String(event.toolName)}: ${rows.map((row) => [row.title, row.meta].filter(Boolean).join(" · ")).join("; ")}`.slice(0, 2_000));
            if (typeof event.result === "string") run.toolResults.push(`${String(event.toolName)} → ${readable(event.result).slice(0, 8_000)}`);
          }
          if (event.type === "screen.request") {
            const request = event.screenRequest as { kind?: string } | undefined;
            run.screenRequests.push(request?.kind ?? "screen");
          }
          // An approval card for an action the task asked for is approved, as the person would.
          // Misty re-sends a card when its state changes, so only a pending one is answered, once.
          const card = event.appRequest as { id?: string; kind?: string; subject?: string; state?: string } | undefined;
          if (event.type === "app.request" && card?.kind === "approve" && card.id && approve.includes(card.subject ?? "")) {
            if (card.state === "pending" && !approved.has(card.id)) {
              approved.add(card.id);
              await this.json(`/me/app-requests/${encodeURIComponent(card.id)}`, { method: "POST", body: JSON.stringify({ decision: "continue" }) });
              run.events.push(`approved:${card.subject}`);
            }
            continue;
          }
          // Nobody answers other cards in an eval: record the card and stop the run
          // rather than waiting out the timeout.
          if (event.type === "approval.required" || event.type === "app.request" || event.type === "agent.request") {
            run.terminal = `needs_person:${event.type}`;
            await reader.cancel();
            await this.raw(created.eventsUrl.replace(/\/events$/, "/cancel"), { method: "POST" }).catch(() => undefined);
            return run;
          }
          if (/^invocation\.(completed|failed|canceled)$/.test(event.type)) {
            run.terminal = event.type;
            await reader.cancel();
            return run;
          }
        }
      }
      run.terminal = "stream.ended";
      return run;
    } catch (error) {
      run.terminal = controller.signal.aborted ? "timeout" : `error: ${String(error).slice(0, 200)}`;
      return run;
    } finally {
      clearTimeout(timer);
    }
  }
}
