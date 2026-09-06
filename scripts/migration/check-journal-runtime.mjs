// Cross-application verification: build the API and managed Worker before running.
// Keys, rooms and documents are disposable; no remote services are contacted.
import assert from "node:assert/strict";
import { generateKeyPairSync, randomBytes } from "node:crypto";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { createCollaborationTickets } from "../../dist/apps/api/src/modules/collaboration/tickets.js";
import { createControlSender } from "../../dist/apps/api/src/modules/collaboration/control.js";

const workerRequire = createRequire(new URL("../../apps/journal-collab/package.json", import.meta.url));
const { Miniflare, Response: WorkerResponse } = workerRequire("miniflare");
// Resolve both through the Worker's CommonJS dependency graph so every provider
// uses the same Yjs instance (mixing its ESM/CJS instances invalidates the test).
const { default: YProvider } = workerRequire("y-partyserver/provider");
const Y = workerRequire("yjs");
const keys = generateKeyPairSync("ed25519");
const config = {
  origin: "http://localhost", privateKey: keys.privateKey,
  roomSalt: randomBytes(32), controlSecret: randomBytes(32),
  projectionSecret: randomBytes(32), previousProjectionSecret: null,
  issuer: "misty-api", audience: "misty-journal-collab",
};
const runtime = new Miniflare({
  modules: true,
  scriptPath: fileURLToPath(new URL("../../apps/journal-collab/dist/index.js", import.meta.url)),
  compatibilityDate: "2026-06-25", compatibilityFlags: ["nodejs_compat"],
  durableObjects: {
    NOTE_ROOM: { className: "NoteRoom", useSQLite: true },
    DRAWING_ROOM: { className: "DrawingRoom", useSQLite: true },
  },
  bindings: {
    JOURNAL_COLLAB_TICKET_PUBLIC_KEY: keys.publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64"),
    JOURNAL_COLLAB_CONTROL_SECRET: config.controlSecret.toString("base64"),
    JOURNAL_COLLAB_PROJECTION_SECRET: config.projectionSecret.toString("base64"),
    JOURNAL_COLLAB_ISSUER: config.issuer, JOURNAL_COLLAB_AUDIENCE: config.audience,
    MISTY_INTERNAL_API_BASE: "https://api.invalid",
  },
  outboundService: () => new WorkerResponse(JSON.stringify({ applied: true }), {
    headers: { "Content-Type": "application/json" },
  }),
});
const providers = [], documents = [];
async function waitFor(predicate, description) {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await delay(20);
  }
  throw new Error(`Journal runtime timed out: ${description}`);
}
try {
  config.origin = (await runtime.ready).origin;
  const mint = createCollaborationTickets(config);
  for (const kind of ["note", "drawing"]) {
    const identity = { kind, id: `${kind}-native-runtime`, spaceId: "space-native-runtime", aclVersion: 1 };
    if (kind === "note") await createControlSender(config, "hosted")(kind, identity.id, "bootstrap", { title: "Native template", markdown: "# Native template" });
    const editorTicket = await mint({ ...identity, userId: "editor", role: "editor" });
    async function connect(ticket) {
      const doc = new Y.Doc(); documents.push(doc);
      const url = new URL(ticket.url);
      assert.equal(url.pathname, `/parties/${kind}-room/${ticket.room}`);
      const provider = new YProvider(url.host, ticket.room, doc, {
        party: `${kind}-room`, protocol: "ws", disableBc: true,
        WebSocketPolyfill: WebSocket, params: { ticket: ticket.ticket },
      });
      providers.push(provider);
      await waitFor(() => provider.synced, `${kind} synchronization`);
      return { doc, provider };
    }
    const editor = await connect(editorTicket);
    if (kind === "note") {
      assert.equal(editor.doc.getText("misty:title").toString(), "Native template");
      editor.doc.getText("misty:title").insert(0, "Edited ");
      await createControlSender(config, "hosted")(kind, identity.id, "bootstrap", { title: "Native template", markdown: "# Native template" });
    }
    const viewer = await connect(await mint({ ...identity, userId: "viewer", role: "viewer" }));
    if (kind === "note") assert.equal(viewer.doc.getText("misty:title").toString(), "Edited Native template");
    editor.doc.getMap("native:proof").set("allowed", `${kind} editor content`);
    await waitFor(() => viewer.doc.getMap("native:proof").get("allowed") === `${kind} editor content`, `${kind} editor propagation`);
    viewer.doc.getMap("native:proof").set("forbidden", "viewer mutation");
    // Awareness is allowed for viewers. Observing this later frame establishes
    // that the server already processed the earlier, forbidden document update.
    viewer.provider.awareness.setLocalStateField("barrier", `${kind}-viewer-frame`);
    await waitFor(() => [...editor.provider.awareness.getStates().values()].some(
      (state) => state.barrier === `${kind}-viewer-frame`), `${kind} viewer frame barrier`);
    const observer = await connect(await mint({ ...identity, userId: "observer", role: "viewer" }));
    assert.equal(observer.doc.getMap("native:proof").get("allowed"), `${kind} editor content`);
    assert.equal(observer.doc.getMap("native:proof").has("forbidden"), false);
    assert.equal(editor.doc.getMap("native:proof").has("forbidden"), false);

    const namespace = await runtime.getDurableObjectNamespace(kind === "note" ? "NOTE_ROOM" : "DRAWING_ROOM");
    const room = namespace.get(namespace.idFromName(editorTicket.room));
    const replay = await room.fetch(`http://room.internal/?export=1&ticket=${encodeURIComponent(editorTicket.ticket)}`);
    assert.equal(replay.status, 401);
    assert.equal((await replay.json()).code, "ticket_replayed");

    const exportTicket = await mint({ ...identity, userId: "account-export", role: "creator" }, true);
    assert.equal(exportTicket.role, "viewer");
    const exportUrl = new URL(exportTicket.url.replace(/^ws/, "http"));
    exportUrl.searchParams.set("export", "1"); exportUrl.searchParams.set("ticket", exportTicket.ticket);
    const downloaded = await fetch(exportUrl);
    assert.equal(downloaded.status, 200);
    const exported = new Y.Doc(); documents.push(exported);
    Y.applyUpdate(exported, new Uint8Array(await downloaded.arrayBuffer()));
    assert.equal(exported.getMap("native:proof").get("allowed"), `${kind} editor content`);
    assert.equal(exported.getMap("native:proof").has("forbidden"), false);
    assert.equal((await fetch(exportUrl)).status, 401);

    for (const entry of [editor, viewer, observer]) entry.provider.shouldConnect = false;
    await createControlSender(config, "hosted")(kind, identity.id, "acl", { acl_version: 2 });
    await waitFor(() => [editor, viewer, observer].every(({ provider }) => !provider.wsconnected), `${kind} stale socket revocation`);
    const stale = await mint({ ...identity, userId: "stale", role: "editor" });
    const denied = await room.fetch(`http://room.internal/?export=1&ticket=${encodeURIComponent(stale.ticket)}`);
    assert.equal(denied.status, 401);
    assert.equal((await denied.json()).code, "ticket_acl_stale");
    const restored = await connect(await mint({ ...identity, aclVersion: 2, userId: "current", role: "editor" }));
    assert.equal(restored.doc.getMap("native:proof").get("allowed"), `${kind} editor content`);
    assert.equal(restored.doc.getMap("native:proof").has("forbidden"), false);
    restored.provider.shouldConnect = false;
    const control = createControlSender(config, "hosted");
    await control(kind, identity.id, "purge", {});
    await waitFor(() => !restored.provider.wsconnected, `${kind} purge disconnection`);
    await control(kind, identity.id, "purge", {}); // Durable retry cannot resurrect content.
    const purgedTicket = await mint({ ...identity, aclVersion: 2, userId: "after-purge", role: "editor" });
    const purged = await room.fetch(`http://room.internal/?export=1&ticket=${encodeURIComponent(purgedTicket.ticket)}`);
    assert.equal(purged.status, 410);
    assert.equal((await purged.json()).code, "resource_deleted");
    console.log(`${kind}: native tickets, editor/viewer synchronization, viewer write denial, single-use account export, replay denial, native ACL revocation and retry-safe purge passed`);
  }
} finally {
  for (const provider of providers) provider.destroy();
  for (const doc of documents) doc.destroy();
  await runtime.dispose();
}
