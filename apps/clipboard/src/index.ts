import { ClipError, MAX_CLIP_BYTES, isSha256, sha256Hex } from "./clips";
import { ClipboardRoom, blobKey, type Env } from "./room";
import { TicketError, isRoomId, verifyTicket, type ClipboardTicketClaims } from "./ticket";

export { ClipboardRoom };

/**
 * Routes:
 *   GET  /v1/rooms/{room}/socket?ticket=…   WebSocket: the clip ring, then new clips
 *   GET  /v1/rooms/{room}/clips             the clip ring
 *   POST /v1/rooms/{room}/clips             announce a clip whose blobs are uploaded
 *   PUT  /v1/rooms/{room}/blobs/{sha256}    upload ciphertext
 *   GET  /v1/rooms/{room}/blobs/{sha256}    download ciphertext, from the edge cache when possible
 * Every request carries a ticket for that room (Authorization: Bearer, or
 * ?ticket= for the WebSocket, which cannot set headers).
 */
const ROUTE = /^\/v1\/rooms\/([0-9a-f]{64})\/(socket|clips|blobs\/([0-9a-f]{64}))$/;
/** Cache keys live on a private name, so a cached blob is only reachable here. */
const CACHE_ORIGIN = "https://clipboard-cache.misty.internal";
const BLOB_CACHE_SECONDS = 24 * 60 * 60;

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    const match = ROUTE.exec(url.pathname);
    if (!match) return json({ code: "not_found" }, 404);
    const [, room = "", action = "", sha = ""] = match;
    if (!isRoomId(room)) return json({ code: "not_found" }, 404);
    let claims: ClipboardTicketClaims;
    try {
      claims = await verifyTicket(ticketFrom(request, url), {
        publicKeyBase64: env.CLIPBOARD_TICKET_PUBLIC_KEY,
        previousPublicKeyBase64: env.CLIPBOARD_TICKET_PUBLIC_KEY_PREVIOUS,
        issuer: env.CLIPBOARD_TICKET_ISSUER,
        audience: env.CLIPBOARD_TICKET_AUDIENCE,
        room,
      });
    } catch (error) {
      const code = error instanceof TicketError ? error.code : "ticket_invalid";
      return json({ code }, code === "ticket_key_misconfigured" ? 500 : 401);
    }
    const stub = env.CLIPBOARD_ROOM.get(env.CLIPBOARD_ROOM.idFromName(room));
    const roomHeaders = { "X-Clipboard-Room": room, "X-Clipboard-Device": claims.device_id };
    try {
      if (action === "socket") {
        if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket")
          return json({ code: "websocket_required" }, 426);
        return stub.fetch("https://room/socket", { headers: { ...roomHeaders, Upgrade: "websocket" } });
      }
      if (action === "clips" && request.method === "GET")
        return noStore(await stub.fetch("https://room/clips", { headers: roomHeaders }));
      if (action === "clips" && request.method === "POST")
        return noStore(
          await stub.fetch("https://room/clips", {
            method: "POST",
            headers: { ...roomHeaders, "Content-Type": "application/json" },
            body: await request.text(),
          }),
        );
      if (isSha256(sha) && request.method === "PUT") return await putBlob(request, env, stub, room, sha);
      if (isSha256(sha) && request.method === "GET") return await getBlob(env, ctx, room, sha);
    } catch (error) {
      if (error instanceof ClipError) return json({ code: error.code }, error.status);
      throw error;
    }
    return json({ code: "method_not_allowed" }, 405);
  },
} satisfies ExportedHandler<Env>;

function ticketFrom(request: Request, url: URL): string {
  const header = request.headers.get("Authorization") ?? "";
  if (header.startsWith("Bearer ")) return header.slice(7).trim();
  return url.searchParams.get("ticket") ?? "";
}

async function putBlob(
  request: Request,
  env: Env,
  stub: DurableObjectStub<ClipboardRoom>,
  room: string,
  sha: string,
): Promise<Response> {
  const declared = Number(request.headers.get("Content-Length") ?? "NaN");
  if (!Number.isSafeInteger(declared) || declared <= 0) return json({ code: "length_required" }, 411);
  if (declared > MAX_CLIP_BYTES) return json({ code: "clip_too_large" }, 413);
  const key = blobKey(room, sha);
  // Content-addressed: the same ciphertext is stored once.
  if (await env.CLIPS.head(key)) return json({ sha256: sha, stored: false }, 200);
  const reserved = await stub.fetch("https://room/reserve", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ bytes: declared }),
  });
  if (!reserved.ok) return noStore(reserved);
  const bytes = await request.arrayBuffer();
  if (bytes.byteLength !== declared) return json({ code: "length_mismatch" }, 400);
  if ((await sha256Hex(bytes)) !== sha) return json({ code: "sha256_mismatch" }, 400);
  await env.CLIPS.put(key, bytes, {
    httpMetadata: { contentType: "application/octet-stream" },
  });
  return json({ sha256: sha, stored: true }, 201);
}

async function getBlob(env: Env, ctx: ExecutionContext, room: string, sha: string): Promise<Response> {
  const cache = caches.default;
  const cacheKey = new Request(`${CACHE_ORIGIN}/${blobKey(room, sha)}`);
  const cached = await cache.match(cacheKey);
  if (cached) return privateBlob(cached.body, "hit");
  const object = await env.CLIPS.get(blobKey(room, sha));
  if (!object) return json({ code: "blob_not_found" }, 404);
  const [forCache, forClient] = object.body.tee();
  // The blob is immutable ciphertext named by its hash, so the edge keeps it
  // for the clip's lifetime. Readers still need a ticket for this room.
  ctx.waitUntil(
    cache.put(
      cacheKey,
      new Response(forCache, {
        headers: {
          "Content-Type": "application/octet-stream",
          "Content-Length": String(object.size),
          "Cache-Control": `public, max-age=${BLOB_CACHE_SECONDS}, immutable`,
        },
      }),
    ),
  );
  return privateBlob(forClient, "miss");
}

function privateBlob(body: ReadableStream | null, cacheStatus: "hit" | "miss"): Response {
  return new Response(body, {
    headers: {
      "Content-Type": "application/octet-stream",
      "Cache-Control": `private, max-age=${BLOB_CACHE_SECONDS}, immutable`,
      "X-Clipboard-Cache": cacheStatus,
    },
  });
}

function noStore(response: Response): Response {
  const copy = new Response(response.body, response);
  copy.headers.set("Cache-Control", "no-store");
  return copy;
}

function json(body: unknown, status: number): Response {
  return Response.json(body, { status, headers: { "Cache-Control": "no-store" } });
}
