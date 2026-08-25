function errorRecord(error: unknown): Record<string, unknown> {
  return typeof error === "object" && error !== null
    ? (error as Record<string, unknown>)
    : {};
}

export function classifyMCPTransportError(error: unknown): {
  code: string;
  message: string;
  transient: boolean;
  recognized: boolean;
  retryAfterMs: number;
} {
  const record = errorRecord(error);
  const name = String(record.name ?? "");
  const detail = String(record.message ?? error ?? "").toLowerCase();
  if (name === "ProtocolError") {
    const unavailable = detail.includes("unknown tool");
    return {
      code: unavailable ? "tool_unavailable" : "mcp_protocol_error",
      message: unavailable
        ? "That tool is not available for this run."
        : "Misty's tool service rejected the request protocol.",
      transient: false,
      recognized: true,
      retryAfterMs: 0,
    };
  }
  const status = Number(record.status ?? 0);
  if (name === "SdkHttpError" || (status >= 400 && status <= 599)) {
    const transient = status === 408 || status === 429 || status >= 500;
    return {
      code:
        status === 429
          ? "rate_limited"
          : transient
            ? "tool_service_unavailable"
            : status === 401 || status === 403
              ? "permission_denied"
              : "mcp_transport_error",
      message:
        status === 429
          ? "Misty's tool service is busy. Please try again shortly."
          : transient
            ? "Misty's tool service is temporarily unavailable."
            : status === 401 || status === 403
              ? "This run is no longer authorized to use that tool."
              : "Misty's tool service rejected the request.",
      transient,
      recognized: true,
      retryAfterMs: status === 429 ? 5_000 : 1_000,
    };
  }
  return {
    code: "tool_transport_failed",
    message: "Misty could not reach its tool service.",
    transient: true,
    recognized: false,
    retryAfterMs: 1_000,
  };
}
