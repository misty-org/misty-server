import { createHash } from "node:crypto";
import { Hono } from "hono";
import { z } from "zod";
import { createAdmission } from "../../../../../packages/runtime/src/admission.js";
import { RuntimeCallbackConflict, type createRuntimeCallbackRepository } from "./runtime-repository.js";
import { verifyRuntimeSignature } from "./runtime-signature.js";

const activation = z.object({ runtime_run_id: z.string().min(1).max(512), runtime_kind: z.string().min(1).max(128) });
// The workflow also sends attempt/progress/input/error_message; they remain in
// the signed body digest but do not become invocation stream payloads.
const nodeEvent = z.object({ runtime_run_id: z.string().min(1).max(512), node_id: z.string().min(1).max(512),
  state: z.enum(["running", "completed", "failed"]), phase: z.string().max(512).default(""),
  output: z.record(z.string(), z.unknown()).default({}) });

async function readBody(request: Request, deadlineMs: number): Promise<Buffer> {
  const reader = request.body?.getReader();
  if (!reader) return Buffer.alloc(0);
  const signal = AbortSignal.any([request.signal, AbortSignal.timeout(deadlineMs)]);
  const cancel = () => { void reader.cancel().catch(() => undefined); };
  signal.addEventListener("abort", cancel, { once: true });
  const chunks: Uint8Array[] = []; let size = 0;
  try {
    for (;;) {
      signal.throwIfAborted();
      const next = await reader.read();
      signal.throwIfAborted();
      if (next.done) return Buffer.concat(chunks, size);
      size += next.value.byteLength;
      if (size > 2 * 1024 * 1024) throw new Error("Runtime body limit");
      chunks.push(next.value);
    }
  } finally { signal.removeEventListener("abort", cancel); cancel(); }
}

export function createRuntimeCallbackRoutes(options: {
  secrets: readonly Uint8Array[];
  repository: ReturnType<typeof createRuntimeCallbackRepository>;
  now?: () => number;
  bodyDeadlineMs?: number;
}) {
  if (options.secrets.some(secret => secret.length < 32)) throw new Error("Invalid runtime signing secret");
  const app = new Hono(), admission = createAdmission(8);
  app.use("*", async (c, next) => {
    c.header("Cache-Control", "no-store");
    return admission.run(next, () => c.json({ code: "agent_runtime_busy" }, 503));
  });
  app.onError((error, c) => {
    if (error instanceof RuntimeCallbackConflict) return c.json({ code: "runtime_callback_conflict" }, 409);
    throw error;
  });
  for (const action of ["activate", "events"] as const) {
    app.post(`/internal/agent-runtime/runs/:runID/${action}`, async c => {
      if (!options.secrets.length) return c.json({ code: "agent_runtime_disabled" }, 503);
      let body: Buffer;
      try { body = await readBody(c.req.raw, options.bodyDeadlineMs ?? 10_000); }
      catch { return c.json({ code: "agent_runtime_unauthorized" }, 401); }
      if (!verifyRuntimeSignature({ method: c.req.method, path: new URL(c.req.url).pathname,
        timestamp: c.req.header("x-misty-agent-timestamp")?.trim() ?? "",
        signature: c.req.header("x-misty-agent-signature")?.trim() ?? "", body,
        secrets: options.secrets, now: options.now?.() ?? Date.now() })) return c.json({ code: "agent_runtime_unauthorized" }, 401);
      if (!c.req.header("idempotency-key")?.trim()) return c.json({ code: "idempotency_key_required" }, 400);
      let json: unknown;
      try { json = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body)); }
      catch { return c.json({ code: "invalid_json" }, 400); }
      const hash = createHash("sha256").update(body).digest("hex"), id = c.req.param("runID");
      if (action === "activate") {
        const parsed = activation.safeParse(json);
        if (!parsed.success) return c.json({ code: "invalid_request" }, 400);
        const result = await options.repository.activate(id, parsed.data, hash);
        return result ? c.json(result) : c.json({ code: "runtime_unavailable" }, 409);
      }
      const parsed = nodeEvent.safeParse(json);
      if (!parsed.success) return c.json({ code: "invalid_request" }, 400);
      const result = await options.repository.event(id, parsed.data, hash);
      return result ? c.json(result) : c.json({ code: "runtime_unavailable" }, 409);
    });
  }
  return app;
}
