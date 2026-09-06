import { mistyServerContracts } from "@misty/contracts";
import { rpcHeaders } from "./forwarding.js";
import type { AppRuntimeDependencies } from "./routes.js";
import { withNativeBody } from "./native-body.js";
type NativeTarget = { methods: ReadonlySet<string>; validatedBodies?: ReadonlySet<string>; request: (request: Request) => Response | Promise<Response> };
/** Route selection happens before execution. Never retry a mutation against another owner. */
export function createNativeDispatcher(targets: NativeTarget[]): NonNullable<AppRuntimeDependencies["dispatch"]> {
  const registry = new Map<string, NativeTarget>();
  for (const target of targets) for (const method of target.methods) {
    if (registry.has(method)) throw new Error(`Duplicate native RPC handler: ${method}`);
    registry.set(method, target);
  }
  return async (request, _session, original) => {
    const target = registry.get(request.method);
    if (!target) return Response.json({ code: "method_not_migrated" }, { status: 501 });
    const headers = rpcHeaders(request.method, original);
    if (!headers) return Response.json({ code: "invalid_request" }, { status: 400 });
    const contract = mistyServerContracts[request.method], parameters = request.params as { path: Record<string, string>; query?: Record<string, string | number | boolean | undefined>; body?: unknown };
    const path = contract.path.replace(/\{([^}]+)\}/g, (_match, name: string) => encodeURIComponent(parameters.path[name]!));
    const url = new URL(path, "http://native-rpc.invalid");
    for (const [key, value] of Object.entries(parameters.query ?? {})) if (value !== undefined) url.searchParams.set(key, String(value));
    const sharedBody = parameters.body !== undefined && target.validatedBodies?.has(request.method);
    const forwarded = new Request(url, { method: contract.verb, headers, signal: original.signal,
      ...(!sharedBody && parameters.body !== undefined ? { body: JSON.stringify(parameters.body) } : {}) });
    return sharedBody ? withNativeBody(forwarded, request.method, parameters.body, () => target.request(forwarded)) : target.request(forwarded);
  };
}
