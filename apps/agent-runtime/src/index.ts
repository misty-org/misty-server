import express, { type Request } from "express";
import { vercelHarness as harness } from "./vercel-harness.js";
import { MISTY_HARNESS_VERSION } from "./harness.js";
import { controlPlaneURL } from "./control-plane.js";
import {
  decodeControlSecret,
  signatureHeaders,
  verifyRequest,
} from "./signature.js";

interface RawRequest extends Request {
  rawBody?: Buffer;
}

const app = express();
app.use(
  express.json({
    limit: "2mb",
    verify: (request, _response, buffer) => {
      (request as RawRequest).rawBody = Buffer.from(buffer);
    },
  }),
);

function reportRuntimeRouteError(operation: string, error: unknown): void {
  console.error(`[misty-agent-runtime] ${operation} failed`, error);
}

function authorized(request: RawRequest): boolean {
  const previousValue =
    process.env.MISTY_AGENT_RUNTIME_CONTROL_SECRET_PREVIOUS?.trim();
  const previousSecret = previousValue
    ? Buffer.from(previousValue, "base64")
    : undefined;
  return verifyRequest({
    secret: decodeControlSecret(),
    previousSecret,
    method: request.method,
    path: request.path,
    timestamp: request.header(signatureHeaders.timestamp) ?? "",
    signature: request.header(signatureHeaders.signature) ?? "",
    body: request.rawBody ?? Buffer.alloc(0),
  });
}

app.get("/health", (_request, response) =>
  response.json({ ok: true, runtime: "misty-agent-runtime", adapter_version: harness.version }),
);

app.post("/v1/runs", async (request: RawRequest, response) => {
  if (!authorized(request))
    return response.status(401).json({ code: "unauthorized" });
  if (!request.header(signatureHeaders.idempotency))
    return response.status(400).json({ code: "idempotency_key_required" });
  const runId =
    typeof request.body?.run_id === "string" ? request.body.run_id.trim() : "";
  const callbackURL =
    typeof request.body?.callback_url === "string"
      ? request.body.callback_url.trim()
      : "";
  // Both creator-owned Agent runs and built-in Misty invocations use the same
  // durable WorkflowAgent. The opaque prefix tells the Go control plane which
  // authorization/data record owns the run; it does not change execution.
  if (!/^(?:run|invocation)_[0-9a-f-]{36}$/.test(runId))
    return response.status(400).json({ code: "invalid_run_id" });
  const adapterVersion = request.body?.adapter_version ?? MISTY_HARNESS_VERSION;
  if (adapterVersion !== harness.version) return response.status(409).json({ code: "harness_version_unavailable" });
  try {
    const run = await harness.start({ mistyRunId: runId, controlPlaneURL: controlPlaneURL(callbackURL), adapterVersion });
    return response.status(202).json({ runtime_run_id: run.runtimeRunId, adapter_version: harness.version });
  } catch (error) {
    reportRuntimeRouteError("workflow start", error);
    const unconfirmed = error instanceof Error && error.message.startsWith("workflow_start_unconfirmed:");
    return response.status(503).json({
      code: unconfirmed ? "workflow_start_unconfirmed" : "workflow_start_failed",
      message: unconfirmed
        ? "The original workflow start is not yet confirmed. Misty will not submit a duplicate."
        : "Misty could not confirm the workflow start.",
    });
  }
});

app.post(
  "/v1/runs/:runtimeRunId/status",
  async (request: RawRequest, response) => {
    if (!authorized(request))
      return response.status(401).json({ code: "unauthorized" });
    const id = request.params.runtimeRunId;
    if (typeof id !== "string" || !id)
      return response.status(400).json({ code: "invalid_runtime_run_id" });
    try {
      return response.json({ status: await harness.status(id) });
    } catch (error) {
      reportRuntimeRouteError("workflow status", error);
      return response.status(503).json({ code: "workflow_status_unavailable" });
    }
  },
);

app.post(
  "/v1/runs/:runtimeRunId/cancel",
  async (request: RawRequest, response) => {
    if (!authorized(request))
      return response.status(401).json({ code: "unauthorized" });
    if (!request.header(signatureHeaders.idempotency))
      return response.status(400).json({ code: "idempotency_key_required" });
    const runtimeRunId = Array.isArray(request.params.runtimeRunId)
      ? request.params.runtimeRunId[0]
      : request.params.runtimeRunId;
    if (!runtimeRunId)
      return response.status(400).json({ code: "invalid_runtime_run_id" });
    try {
      await harness.cancel(runtimeRunId);
      return response.json({ canceled: true });
    } catch (error) {
      reportRuntimeRouteError("workflow cancellation", error);
      return response.status(503).json({
        code: "workflow_cancel_failed",
        message: "Misty could not cancel this work. Please try again shortly.",
      });
    }
  },
);

app.post("/v1/approvals/:hookToken", async (request: RawRequest, response) => {
  if (!authorized(request))
    return response.status(401).json({ code: "unauthorized" });
  if (!request.header(signatureHeaders.idempotency))
    return response.status(400).json({ code: "idempotency_key_required" });
  const hookToken = Array.isArray(request.params.hookToken)
    ? request.params.hookToken[0]
    : request.params.hookToken;
  if (
    !hookToken ||
    typeof request.body?.approved !== "boolean" ||
    typeof request.body?.approval_id !== "string"
  ) {
    return response.status(400).json({ code: "invalid_approval_resume" });
  }
  try {
    await harness.resume({ kind: "approval", token: hookToken, approved: request.body.approved, approvalId: request.body.approval_id });
    return response.json({ resumed: true });
  } catch (error) {
    reportRuntimeRouteError("approval resumption", error);
    return response.status(409).json({
      code: "approval_resume_failed",
      message: "This approval can no longer be resumed.",
    });
  }
});

app.post("/v1/devices/:hookToken", async (request: RawRequest, response) => {
  if (!authorized(request))
    return response.status(401).json({ code: "unauthorized" });
  if (!request.header(signatureHeaders.idempotency))
    return response.status(400).json({ code: "idempotency_key_required" });
  const hookToken = Array.isArray(request.params.hookToken)
    ? request.params.hookToken[0]
    : request.params.hookToken;
  if (!hookToken || typeof request.body?.available !== "boolean") {
    return response.status(400).json({ code: "invalid_device_resume" });
  }
  try {
    await harness.resume({ kind: "device", token: hookToken, available: request.body.available });
    return response.json({ resumed: true });
  } catch (error) {
    reportRuntimeRouteError("device resumption", error);
    return response.status(409).json({
      code: "device_resume_failed",
      message: "This device request can no longer be resumed.",
    });
  }
});

export default app;
