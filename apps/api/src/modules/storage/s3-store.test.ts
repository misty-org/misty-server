import { createServer } from "node:http";
import { once } from "node:events";
import { createHash } from "node:crypto";
import { expect, it } from "vitest";
import { createS3Store } from "./s3-store.js";
import { loadS3Config } from "./s3-config.js";

const config = { endpoint: "https://account.r2.cloudflarestorage.com", region: "auto", bucket: "misty-private",
  accessKeyId: "test-only-access", secretAccessKey: "test-only-secret", forcePathStyle: true };
it("signs the exact key, size, MIME and checksum without exposing Misty credentials", async () => {
  const now = new Date("2026-09-05T12:00:00Z"), store = createS3Store(config, () => now);
  try {
    const transfer = await store.signUpload("library/12345678", { byteSize: 123, mimeType: "image/png", sha256: "a".repeat(64) }, new Date(now.getTime() + 900000));
    const url = new URL(transfer.url), signed = url.searchParams.get("X-Amz-SignedHeaders")!.split(";");
    expect(url.pathname).toBe("/misty-private/library/12345678");
    expect(url.searchParams.get("X-Amz-Expires")).toBe("900");
    expect(signed).toEqual(expect.arrayContaining(["content-type", "content-length", "x-amz-checksum-sha256", "x-amz-meta-misty-library-sha256"]));
    expect(url.searchParams.has("x-amz-checksum-sha256")).toBe(false);
    expect(transfer.headers).toEqual({ "Content-Type": "image/png", "x-amz-checksum-sha256": Buffer.from("a".repeat(64), "hex").toString("base64"), "x-amz-meta-misty-library-sha256": "a".repeat(64) });
    expect(transfer.url).not.toContain(config.secretAccessKey);
    const download = await store.signDownload("library/12345678", "../résumé\r\n.png", new Date(now.getTime() + 120000));
    expect(download.filename).toBe("résumé.png");
    expect(new URL(download.url).searchParams.get("response-content-disposition")).toContain("filename*=UTF-8''r%C3%A9sum%C3%A9.png");
    await expect(store.signUpload("../private", { byteSize: 1, sha256: "a".repeat(64), mimeType: "image/png" }, new Date(now.getTime()+900000))).rejects.toThrow();
    await expect(store.signDownload("library/12345678", "x", new Date(now.getTime()+3601000))).rejects.toThrow();
  } finally { store.close(); }
});
it("uses authenticated bounded HEAD/DELETE requests and distinguishes missing objects", async () => {
  const seen: string[] = [];
  const server = createServer((request, response) => {
    expect(request.headers.authorization).toContain("AWS4-HMAC-SHA256"); seen.push(request.method!);
    if (request.url?.includes("missing00")) { response.writeHead(404); response.end(); return; }
    if (request.method === "DELETE") { response.writeHead(204); response.end(); return; }
    response.writeHead(200, { "Content-Length": "20", "Content-Type": "image/png", "x-amz-meta-misty-library-sha256": "a".repeat(64) }); response.end();
  });
  server.listen(0, "127.0.0.1"); await once(server, "listening");
  const address = server.address() as { port: number }, store = createS3Store({ ...config, endpoint: `http://127.0.0.1:${address.port}` });
  try {
    expect(await store.head("library/12345678")).toEqual({ byteSize: 20, sha256: "a".repeat(64), mimeType: "image/png" });
    expect(await store.head("library/missing00")).toBeNull();
    await store.delete("library/12345678");
    expect(seen).toEqual(["HEAD", "HEAD", "DELETE"]);
    await expect(store.head("library/12345678", AbortSignal.abort())).rejects.toThrow("Object storage is unavailable");
  } finally { store.close(); server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); }
});
it("validates hosted and self-host S3 origins without echoing secrets", () => {
  const env = { R2_ENDPOINT: config.endpoint, R2_BUCKET: config.bucket, R2_ACCESS_KEY: config.accessKeyId, R2_SECRET_KEY: config.secretAccessKey };
  expect(loadS3Config({})).toBeNull(); expect(loadS3Config(env)).toEqual(config);
  for (const endpoint of ["http://r2.example", "https://user:secret@r2.example", "https://r2.example/path", "https://r2.example?secret=private"]) {
    expect(() => loadS3Config({ ...env, R2_ENDPOINT: endpoint })).toThrow("Invalid object storage configuration");
  }
  expect(loadS3Config({ ...env, R2_ENDPOINT: "http://127.0.0.1:9000", MISTY_DEPLOYMENT_MODE: "self_hosted" })?.endpoint).toBe("http://127.0.0.1:9000");
  expect(() => loadS3Config({ R2_SECRET_KEY: "private" })).toThrow("Invalid object storage configuration");
});

it("streams authenticated PUT/GET bytes with exact checksum metadata and missing-object semantics", async () => {
  const data = Buffer.from("private avatar bytes"), sha256 = createHash("sha256").update(data).digest("hex");
  const seen: { method: string; auth: string; data: Buffer; headers: import("node:http").IncomingHttpHeaders }[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = []; for await (const chunk of request) chunks.push(Buffer.from(chunk));
    seen.push({ method: request.method!, auth: request.headers.authorization ?? "", data: Buffer.concat(chunks), headers: request.headers });
    if (request.url?.includes("missing00")) { response.writeHead(404); response.end(); return; }
    if (request.method === "PUT") { response.writeHead(200); response.end(); return; }
    response.writeHead(200, { "Content-Length": data.length, "Content-Type": "image/png", "x-amz-meta-misty-library-sha256": sha256 }); response.end(data);
  });
  server.listen(0, "127.0.0.1"); await once(server, "listening");
  const store = createS3Store({ ...config, endpoint: `http://127.0.0.1:${(server.address() as { port: number }).port}` });
  try {
    await store.putBytes("avatars/avatar_12345678", data, { byteSize: data.length, mimeType: "image/png", sha256 });
    expect(await store.getBytes("avatars/avatar_12345678", 100)).toEqual(data);
    expect(await store.getBytes("avatars/missing00", 100)).toBeNull();
    await expect(store.putBytes("avatars/missing00", data, { byteSize: data.length, mimeType: "image/png", sha256 })).rejects.toThrow("Object storage is unavailable");
    expect(seen.map((entry) => entry.method)).toEqual(["PUT", "GET", "GET", "PUT"]);
    expect(seen.every((entry) => entry.auth.includes("AWS4-HMAC-SHA256"))).toBe(true);
    expect(seen[0]!.data).toEqual(data);
    expect(seen[0]!.headers).toMatchObject({ "content-length": String(data.length), "content-type": "image/png", "x-amz-checksum-sha256": Buffer.from(sha256, "hex").toString("base64"), "x-amz-meta-misty-library-sha256": sha256 });
    await expect(store.putBytes("avatars/avatar_87654321", data, { byteSize: data.length, mimeType: "image/png", sha256: "a".repeat(64) })).rejects.toThrow("Object storage is unavailable");
    expect(seen).toHaveLength(4);
  } finally { store.close(); server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); }
});
it("rejects declared or streamed oversize and checksum corruption without buffering unlimited response bodies", async () => {
  const server = createServer((request, response) => {
    if (request.url?.includes("declared")) { response.writeHead(200, { "Content-Length": 1000000000 }); response.flushHeaders(); return; }
    if (request.url?.includes("checksum")) { response.writeHead(200, { "x-amz-meta-misty-library-sha256": "a".repeat(64) }); response.end("bad"); return; }
    response.writeHead(200); response.write(Buffer.alloc(101)); response.end();
  });
  server.listen(0, "127.0.0.1"); await once(server, "listening");
  const store = createS3Store({ ...config, endpoint: `http://127.0.0.1:${(server.address() as { port: number }).port}` });
  try {
    for (const key of ["avatars/declared1", "avatars/streamed1", "avatars/checksum1"]) {
      await expect(store.getBytes(key, 100)).rejects.toThrow("Object storage is unavailable");
    }
    for (const limit of [0, NaN, 16 * 1024 * 1024 + 1]) await expect(store.getBytes("avatars/validkey1", limit)).rejects.toThrow("Object storage is unavailable");
  } finally { store.close(); server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); }
});
it("keeps admission occupied until streaming ends, aborts stalled bodies and releases every permit", async () => {
  let signalReady!: () => void;
  const ready = new Promise<void>((resolve) => { signalReady = resolve; });
  let seen = 0;
  const server = createServer((request, response) => {
    if (request.url?.includes("complete")) { response.end("done"); return; }
    response.writeHead(200); response.write("held");
    if (++seen === 16) signalReady();
  });
  server.listen(0, "127.0.0.1"); await once(server, "listening");
  const store = createS3Store({ ...config, endpoint: `http://127.0.0.1:${(server.address() as { port: number }).port}` });
  const controllers = Array.from({ length: 16 }, () => new AbortController());
  const pending = controllers.map((controller, index) => store.getBytes(`avatars/stream_${index.toString().padStart(8, "0")}`, 100, controller.signal).then(() => "unexpected success", (error: Error) => error.message));
  try {
    await ready;
    await expect(store.getBytes("avatars/complete1", 100)).rejects.toThrow("Object storage is unavailable");
    controllers.forEach((controller) => controller.abort());
    expect(await Promise.all(pending)).toEqual(Array(16).fill("Object storage is unavailable"));
    expect(await store.getBytes("avatars/complete1", 100)).toEqual(Buffer.from("done"));
    expect(seen).toBe(16);
  } finally { controllers.forEach((controller) => controller.abort()); store.close(); server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); await Promise.all(pending); }
});
