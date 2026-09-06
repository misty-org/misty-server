const bodies = new WeakMap<Request, { method: string; body: unknown }>();

/** Process-private handoff after public contract validation. No HTTP header or
 * caller-controlled object can mark an external request as already validated. */
export function withNativeBody<T>(request: Request, method: string, body: unknown, dispatch: () => T | Promise<T>): Promise<T> {
  bodies.set(request, { method, body });
  return Promise.resolve().then(dispatch).finally(() => bodies.delete(request));
}
export function nativeBody(request: Request, method: string): unknown {
  const value = bodies.get(request);
  return value?.method === method ? value.body : undefined;
}
