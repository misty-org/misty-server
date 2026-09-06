const uploadHeader = "X-Misty-Library-Upload-Token";
const finalizeMethods = new Set(["notes.assets.finalize", "drawings.assets.finalize"]);

/** Credentials stay in the trusted host transport, never the public RPC body. */
export function rpcHeaders(method: string, original: Request): Headers | null {
  const headers = new Headers({ Authorization: original.headers.get("Authorization") ?? "", "Content-Type": "application/json" });
  if (finalizeMethods.has(method) && original.headers.has(uploadHeader)) {
    const token = original.headers.get(uploadHeader)!.trim();
    // Commas also reject combined duplicate header values. Existing upload
    // credentials are opaque printable tokens, bounded independently of bodies.
    if (!token || token.length > 1024 || /[^\x21-\x7e]|,/.test(token)) return null;
    headers.set(uploadHeader, token);
  }
  return headers;
}
