import "./network.js";
import express, { type Request } from "express";
import { rateLimit } from "express-rate-limit";
import { scrubCredential } from "./model-provider.js";
import { desktopRuntime, deviceSecrets, setDeviceModels } from "./device-models.js";
import {
  cleanupCall, daySummaryDeviceCall, diarizedDeviceCall, embedDeviceCall, extractCall, refinePointCall, screenModelCall,
  modelTestCall, voiceNotesDeviceCall, voiceTranscriptionCall,
} from "./device-calls.js";
import { followUpCall, restoreStepCall } from "./device-routing.js";
import { modelCallFailure } from "./model-calls.js";
import { workflowHarness as harness } from "./workflow-harness.js";
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
// Throttle before body parsing and signature verification. Only failed requests (bad
// signatures, invalid input) count: floods without the launch secret are stopped while the
// app's own calls are never throttled here.
app.use("/v1", rateLimit({
  windowMs: 60_000,
  limit: 600,
  skipSuccessfulRequests: true,
  standardHeaders: "draft-8",
  legacyHeaders: false,
  message: { code: "rate_limited" },
}));
const keepRawBody = (request: unknown, _response: unknown, buffer: Buffer) => {
  (request as RawRequest).rawBody = Buffer.from(buffer);
};
// The desktop's model calls carry screenshots and recordings, so they get a larger body than
// control calls. They parse first; the general parser skips an already parsed body.
app.use("/v1/device", express.json({ limit: "40mb", verify: keepRawBody }));
app.use(express.json({ limit: "2mb", verify: keepRawBody }));

function reportRuntimeRouteError(operation: string, error: unknown): void {
  console.error(`[misty-agent-runtime] ${operation} failed`, error);
}

function authorized(request: RawRequest): boolean {
  return verifyRequest({
    secret: decodeControlSecret(),
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

// The desktop app's model keys and choices, signed with this launch's secret
// (src/device-models.ts). Only a runtime started by the app takes them.
app.post("/v1/local/models", (request: RawRequest, response) => {
  if (!desktopRuntime()) return response.status(404).json({ code: "not_found" });
  if (!authorized(request)) return response.status(401).json({ code: "unauthorized" });
  if (!setDeviceModels(request.body)) return response.status(400).json({ code: "invalid_models" });
  return response.json({ ok: true });
});

// Model calls the desktop makes outside runs, on its own models (src/device-calls.ts).
for (const [path, call] of [
  ["/v1/device/cleanup", cleanupCall],
  ["/v1/device/voice-transcription", voiceTranscriptionCall],
  ["/v1/device/screen-model", screenModelCall],
  ["/v1/device/refine-point", refinePointCall],
  ["/v1/device/extract", extractCall],
  ["/v1/device/embed", embedDeviceCall],
  ["/v1/device/day-summary", daySummaryDeviceCall],
  ["/v1/device/voice-notes", voiceNotesDeviceCall],
  ["/v1/device/transcribe-diarized", diarizedDeviceCall],
  ["/v1/device/model-test", modelTestCall],
  ["/v1/device/follow-up", followUpCall],
  ["/v1/device/restore-step", restoreStepCall],
] as const) {
  app.post(path, async (request: RawRequest, response) => {
    if (!desktopRuntime()) return response.status(404).json({ code: "not_found" });
    if (!authorized(request)) return response.status(401).json({ code: "unauthorized" });
    const controller = new AbortController();
    response.on("close", () => {
      if (!response.writableFinished) controller.abort();
    });
    try {
      return response.json(await call(request.body, controller.signal));
    } catch (error) {
      const failure = modelCallFailure(error);
      if (failure.status >= 500) reportRuntimeRouteError(`device call ${path}`, scrubCredential(error, deviceSecrets()));
      return response.status(failure.status).json(failure.body);
    }
  });
}

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
  // A start carries the run's own key and the receipt the server pinned for it.
  const runKey = typeof request.body?.run_key === "string" ? request.body.run_key.trim() : "";
  const receiptCallback = typeof request.body?.receipt_callback === "string" ? request.body.receipt_callback.trim() : "";
  if (!/^[A-Za-z0-9+/]{43}=$/.test(runKey) || !receiptCallback || receiptCallback.length > 512)
    return response.status(400).json({ code: "invalid_run_key" });
  try {
    const run = await harness.start({
      mistyRunId: runId, controlPlaneURL: controlPlaneURL(callbackURL), adapterVersion,
      runKey, receiptCallback,
    });
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
