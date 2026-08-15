import { createHash, createHmac, generateKeyPairSync } from "node:crypto";
import { createServer } from "node:http";
import { get as httpGet } from "node:http";
import { spawn } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";

test("signed controls bootstrap, advance ACL, and purge durable state", async (t) => {
  const internalSecret = "internal-secret-that-is-at-least-32-bytes";
  const controlSecret = Buffer.alloc(32, 7);
  const roomSalt = Buffer.alloc(32, 11);
  let state = null;
  let checksum = "";
  let aclVersion = 0;
  let purgeCount = 0;

  const api = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    if (request.headers["x-misty-internal-secret"] !== internalSecret) {
      return response.writeHead(404).end();
    }
    if (request.method === "GET") {
      if (!state) return response.writeHead(404).end();
      response.writeHead(200, {
        "Content-Type": "application/vnd.yjs.update",
        "X-Content-SHA256": checksum,
        "X-Misty-ACL-Version": String(aclVersion),
      });
      return response.end(state);
    }
    if (request.method === "PUT") {
      state = Buffer.concat(chunks);
      checksum = createHash("sha256").update(state).digest("hex");
      assert.equal(request.headers["x-content-sha256"], checksum);
      aclVersion = Number(request.headers["x-misty-acl-version"]);
      return response.writeHead(204).end();
    }
    if (request.method === "DELETE") {
      state = null;
      purgeCount += 1;
      return response.writeHead(204).end();
    }
    response.writeHead(405).end();
  });
  await listen(api);
  t.after(() => api.close());

  const apiAddress = api.address();
  assert(apiAddress && typeof apiAddress === "object");
  const collabPort = await availablePort();
  const { publicKey } = generateKeyPairSync("ed25519");
  const rawPublicKey = publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64");
  const child = spawn(process.execPath, ["index.mjs"], {
    cwd: new URL(".", import.meta.url),
    env: {
      ...process.env,
      PORT: String(collabPort),
      MISTY_INTERNAL_API_BASE: `http://127.0.0.1:${apiAddress.port}`,
      MISTY_COLLAB_INTERNAL_SECRET: internalSecret,
      JOURNAL_COLLAB_TICKET_PUBLIC_KEY: rawPublicKey,
      JOURNAL_COLLAB_CONTROL_SECRET: controlSecret.toString("base64"),
      JOURNAL_COLLAB_ROOM_SALT: roomSalt.toString("base64"),
    },
    stdio: "ignore",
  });
  t.after(() => child.kill("SIGTERM"));
  await waitForHealth(collabPort);

  const resourceID = "note_123";
  const room = createHmac("sha256", roomSalt)
    .update(`note-room:${resourceID}`)
    .digest("base64url")
    .slice(0, 32);
  const endpoint = `http://127.0.0.1:${collabPort}/parties/note-room/${room}`;

  const forged = await fetch(endpoint, {
    method: "POST",
    headers: controlHeaders(resourceID, Buffer.from("{}"), "forged"),
    body: "{}",
  });
  assert.equal(forged.status, 401);

  const bootstrap = await sendControl(endpoint, resourceID, controlSecret, "bootstrap", {
    title: "Research plan",
    markdown: "# Research plan\nQuestion",
  });
  assert.equal(bootstrap.status, 200);
  assert.equal((await bootstrap.json()).initialized, true);
  assert(state && state.length > 0);

  const acl = await sendControl(endpoint, resourceID, controlSecret, "acl", { acl_version: 4 });
  assert.equal(acl.status, 200);
  assert.equal(aclVersion, 4);

  const status = await sendControl(endpoint, resourceID, controlSecret, "status", {});
  assert.equal(status.status, 200);
  assert.equal((await status.json()).acl_version, 4);

  const purge = await sendControl(endpoint, resourceID, controlSecret, "purge", {});
  assert.equal(purge.status, 200);
  assert.equal(purgeCount, 1);
  assert.equal(state, null);
});

async function sendControl(endpoint, resourceID, secret, command, payload) {
  const body = Buffer.from(JSON.stringify({ command, payload }));
  const timestamp = String(Math.floor(Date.now() / 1000));
  return fetch(endpoint, {
    method: "POST",
    headers: controlHeaders(resourceID, body, signControl(secret, body, timestamp), timestamp),
    body,
  });
}

function controlHeaders(
  resourceID,
  body,
  signature,
  timestamp = String(Math.floor(Date.now() / 1000)),
) {
  return {
    "Content-Type": "application/json",
    "Content-Length": String(body.length),
    "X-Misty-Resource-ID": resourceID,
    "X-Misty-Timestamp": timestamp,
    "X-Misty-Signature": signature,
  };
}

function signControl(secret, body, timestamp) {
  return createHmac("sha256", secret).update(timestamp).update("\n").update(body).digest("base64url");
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
}

function availablePort() {
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.once("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const address = probe.address();
      assert(address && typeof address === "object");
      probe.close(() => resolve(address.port));
    });
  });
}

async function waitForHealth(port) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    const ready = await new Promise((resolve) => {
      const request = httpGet(`http://127.0.0.1:${port}/health`, (response) => {
        response.resume();
        resolve(response.statusCode === 200);
      });
      request.on("error", () => resolve(false));
    });
    if (ready) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("collaboration service did not become ready");
}
