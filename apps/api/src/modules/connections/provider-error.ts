export class ProviderRequestError extends Error {
  constructor(readonly status = 0) { super("Provider request failed"); this.name = "ProviderRequestError"; }
  get code() { return ({ 401: "connection_revoked", 403: "permission_missing", 429: "rate_limited", 404: "not_found", 410: "cursor_expired", 412: "conflict" } as Record<number, string>)[this.status] ?? "provider_error"; }
  get httpStatus() { return this.status === 401 || this.status === 403 ? 424 : this.status === 412 ? 409 : 502; }
}
