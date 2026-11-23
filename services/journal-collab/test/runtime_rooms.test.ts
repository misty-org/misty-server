// This file intentionally exercises the emitted Worker bundle as a black box.
// Miniflare's runtime objects are more dynamic than the Worker source types.
// eslint-disable-next-line @typescript-eslint/ban-ts-comment -- black-box test of the built Worker
// @ts-nocheck
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "vitest";

import * as Y from "yjs";

import {
  awarenessHasUser,
  createRuntime,
  customMessages,
  dropSocket,
  mintTicket,
  providerFor,
  providerSynced,
  waitFor,
} from "./runtime_harness";

function fingerprint(doc) {
  return createHash("sha256").update(Y.encodeSnapshot(Y.snapshot(doc))).digest("base64");
}

function writerMessages(messages) {
  return messages.filter((message) => message.type === "file_writer");
}

function lastWriterMessage(messages) {
  return writerMessages(messages).at(-1);
}

function pause(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function driveVersions(drive) {
  return async (request) => {
    if (request.url.endsWith("/internal/journal/file-versions")) {
      drive.asked.push(JSON.parse(await request.text()));
      if (drive.down) return new Response("unavailable", { status: 503 });
      return new Response(JSON.stringify({ version: drive.version, content_version: drive.content, modified_by: drive.by }));
    }
    return new Response("{}");
  };
}

test("FileRoom saves a delete-only edit when the last editor leaves", async () => {
  const saves = [];
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        saves.push(JSON.parse(await request.text()));
        return new Response(JSON.stringify({ version: saves.length + 3 }));
      }
      return new Response("{}");
    },
  });
  let provider;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text";
    const doc = new Y.Doc();
    provider = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file"), "file-room");
    const messages = customMessages(provider);
    await providerSynced(provider);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    assert.equal(lastWriterMessage(messages).loaded, false);
    assert.equal(lastWriterMessage(messages).read_only, false);
    assert.equal(typeof lastWriterMessage(messages).generation, "string");

    doc.transact(() => {
      doc.getText("text").insert(0, "hello world");
      doc.getMap("misty:file").set("loaded", 3);
    });
    provider.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc) }));
    await waitFor(() => lastWriterMessage(messages)?.loaded === true);
    assert.equal(lastWriterMessage(messages).dirty, false);

    doc.getText("text").delete(0, 6);
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    provider.destroy();
    provider = undefined;

    await waitFor(() => saves.some((save) => save.text === "world"));
    assert.ok(!saves.some((save) => save.text === "hello world"));
    assert.equal(saves.find((save) => save.text === "world").base_version, 3);
  } finally {
    provider?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom hands the writer over when the writer leaves before loading", async () => {
  const runtime = createRuntime();
  let providerA;
  let providerB;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-handover";
    const resource = { resource_id: "space-runtime:item-runtime:app-runtime" };
    const docA = new Y.Doc();
    providerA = providerFor(runtimeUrl, room, docA, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messagesA = customMessages(providerA);
    await providerSynced(providerA);
    await waitFor(() => lastWriterMessage(messagesA)?.writer === true);

    const docB = new Y.Doc();
    providerB = providerFor(runtimeUrl, room, docB, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const messagesB = customMessages(providerB);
    await providerSynced(providerB);
    await waitFor(() => lastWriterMessage(messagesB) !== undefined);
    assert.equal(lastWriterMessage(messagesB).writer, false);
    const generation = lastWriterMessage(messagesB).generation;

    dropSocket(providerA);
    await waitFor(() => lastWriterMessage(messagesB)?.writer === true);
    assert.equal(lastWriterMessage(messagesB).loaded, false);
    assert.equal(lastWriterMessage(messagesB).generation, generation);

    docB.getMap("misty:file").set("loaded", 1);
    await waitFor(() => lastWriterMessage(messagesB)?.loaded === true);
    assert.equal(lastWriterMessage(messagesB).dirty, true);
    providerB.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(docB) }));
    await waitFor(() => lastWriterMessage(messagesB)?.dirty === false);
  } finally {
    providerA?.destroy();
    providerB?.destroy();
    await runtime.dispose();
  }
});

test("a reconnect that reuses its connection id keeps the live socket", async () => {
  const runtime = createRuntime();
  let first;
  let second;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-app-same-id";
    first = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-a", "app"), "app-room", {
      connectionId: "same-connection",
    });
    await providerSynced(first);
    const secondDoc = new Y.Doc();
    second = providerFor(runtimeUrl, room, secondDoc, mintTicket(room, "user-a", "app"), "app-room", {
      connectionId: "same-connection",
    });
    await providerSynced(second);
    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "app"), "app-room");
    await providerSynced(other);

    dropSocket(first);
    await pause(200);
    otherDoc.getText("content").insert(0, "still live");
    await waitFor(() => secondDoc.getText("content").toString() === "still live");
  } finally {
    first?.destroy();
    second?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("presence re-sent after a reconnect leaves with the socket that sent it", async () => {
  const runtime = createRuntime();
  let watcher;
  let first;
  let second;
  let intruder;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-app-presence";
    watcher = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "app"), "app-room");
    await providerSynced(watcher);

    const firstDoc = new Y.Doc();
    firstDoc.clientID = 4242;
    first = providerFor(runtimeUrl, room, firstDoc, mintTicket(room, "user-a", "app"), "app-room");
    first.awareness.setLocalStateField("user", { id: "user-a", name: "Ada" });
    await providerSynced(first);
    await waitFor(() => awarenessHasUser(watcher, "user-a", "Ada"));

    const secondDoc = new Y.Doc();
    secondDoc.clientID = 4242;
    second = providerFor(runtimeUrl, room, secondDoc, mintTicket(room, "user-a", "app"), "app-room", {
      connect: false,
    });
    for (let step = 0; step < 4; step += 1) second.awareness.setLocalStateField("step", step);
    second.awareness.setLocalStateField("user", { id: "user-a", name: "Ada again" });
    second.connect();
    await providerSynced(second);
    await waitFor(() => awarenessHasUser(watcher, "user-a", "Ada again"));

    dropSocket(first);
    await pause(300);
    assert.ok(awarenessHasUser(watcher, "user-a", "Ada again"));

    const intruderDoc = new Y.Doc();
    intruderDoc.clientID = 4242;
    intruder = providerFor(runtimeUrl, room, intruderDoc, mintTicket(room, "user-c", "app"), "app-room", {
      connect: false,
    });
    for (let step = 0; step < 10; step += 1) intruder.awareness.setLocalStateField("step", step);
    intruder.awareness.setLocalStateField("user", { id: "user-c", name: "Eve" });
    intruder.connect();
    await providerSynced(intruder);
    await pause(300);
    assert.ok(!awarenessHasUser(watcher, "user-c", "Eve"));
    assert.ok(awarenessHasUser(watcher, "user-a", "Ada again"));

    dropSocket(second);
    await waitFor(() => !awarenessHasUser(watcher, "user-a", "Ada again"));
  } finally {
    watcher?.destroy();
    first?.destroy();
    second?.destroy();
    intruder?.destroy();
    await runtime.dispose();
  }
});

test("a socket that isn't admitted receives nothing", async () => {
  const runtime = createRuntime();
  let writer;
  let typing;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-app-unadmitted";
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "app"), "app-room");
    writer.awareness.setLocalStateField("user", { id: "user-a", name: "Ada" });
    await providerSynced(writer);
    doc.getText("content").insert(0, "private");
    let step = 0;
    typing = setInterval(() => {
      step += 1;
      doc.getText("content").insert(0, "x");
      writer.awareness.setLocalStateField("step", step);
    }, 2);

    const received = [];
    const closes = await Promise.all(
      Array.from({ length: 12 }, (_, index) => new Promise((resolve) => {
        const ticket = mintTicket("some-other-room", `user-${index}`, "app");
        const socket = new WebSocket(`ws://${runtimeUrl.host}/parties/app-room/${room}?ticket=${encodeURIComponent(ticket)}`);
        socket.binaryType = "arraybuffer";
        socket.addEventListener("message", (event) => received.push(event.data));
        socket.addEventListener("close", (event) => resolve(event));
        socket.addEventListener("error", () => undefined);
      })),
    );
    for (const event of closes) assert.equal(event.code, 1008);
    assert.equal(received.length, 0);
  } finally {
    clearInterval(typing);
    writer?.destroy();
    await runtime.dispose();
  }
});

test("an update larger than the old 512KB ceiling is accepted", async () => {
  const runtime = createRuntime();
  let providerA;
  let providerB;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-app-large";
    const docA = new Y.Doc();
    providerA = providerFor(runtimeUrl, room, docA, mintTicket(room, "user-a", "app"), "app-room");
    const closes = [];
    providerA.on("connection-close", (event) => {
      if (event) closes.push(event.reason);
    });
    await providerSynced(providerA);
    const docB = new Y.Doc();
    providerB = providerFor(runtimeUrl, room, docB, mintTicket(room, "user-b", "app"), "app-room");
    await providerSynced(providerB);

    const large = "x".repeat(600 * 1024);
    docA.getText("content").insert(0, large);
    await waitFor(() => docB.getText("content").length === large.length);
    assert.ok(!closes.includes("message_too_large"));
  } finally {
    providerA?.destroy();
    providerB?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom moves the writer to the same person's newer connection", async () => {
  const runtime = createRuntime();
  let older;
  let newer;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-same-user";
    const resource = { resource_id: "space-runtime:item-same:app-runtime" };
    older = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const olderMessages = customMessages(older);
    await providerSynced(older);
    await waitFor(() => lastWriterMessage(olderMessages)?.writer === true);

    newer = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const newerMessages = customMessages(newer);
    await providerSynced(newer);
    await waitFor(() => lastWriterMessage(newerMessages)?.writer === true);
    await waitFor(() => lastWriterMessage(olderMessages)?.writer === false);
  } finally {
    older?.destroy();
    newer?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom reloads a clean room from the drive when its writer asks, and never a dirty one", async () => {
  const runtime = createRuntime();
  let provider;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-reload";
    const resource = { resource_id: "space-runtime:item-reload:app-runtime" };
    const doc = new Y.Doc();
    provider = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(provider);
    await providerSynced(provider);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;

    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    provider.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.loaded === true && lastWriterMessage(messages)?.dirty === false);

    doc.getMap("content").set("value", 2);
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    provider.sendMessage(JSON.stringify({ type: "file_reload", version: 7 }));
    await pause(500);
    assert.equal(lastWriterMessage(messages).generation, generation);
    assert.equal(doc.getMap("content").get("value"), 2);

    provider.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 5 }));
    await waitFor(() => lastWriterMessage(messages)?.dirty === false && lastWriterMessage(messages)?.base === 5);
    provider.sendMessage(JSON.stringify({ type: "file_reload", version: 5 }));
    await pause(500);
    assert.equal(lastWriterMessage(messages).generation, generation);

    provider.sendMessage(JSON.stringify({ type: "file_reload", version: 7 }));
    await waitFor(() => lastWriterMessage(messages)?.generation !== generation);
    const reloaded = lastWriterMessage(messages);
    assert.equal(reloaded.loaded, false);
    assert.equal(reloaded.has_writer, false);
    await waitFor(() => doc.getMap("content").get("value") === undefined);
    assert.equal(doc.getMap("misty:file").get("loaded"), undefined);
    provider.destroy();

    const freshDoc = new Y.Doc();
    provider = providerFor(runtimeUrl, room, freshDoc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const freshMessages = customMessages(provider);
    await providerSynced(provider);
    await waitFor(() => lastWriterMessage(freshMessages)?.writer === true);
    assert.equal(lastWriterMessage(freshMessages).generation, reloaded.generation);
    assert.equal(lastWriterMessage(freshMessages).loaded, false);
    assert.equal(lastWriterMessage(freshMessages).has_writer, true);
    assert.equal(freshDoc.getMap("content").get("value"), undefined);
    assert.equal(freshDoc.getMap("misty:file").get("loaded"), undefined);
  } finally {
    provider?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom closes every socket of the old generation when it reloads", async () => {
  const runtime = createRuntime();
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-reload-closes";
    const resource = { resource_id: "space-runtime:item-reload-closes:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.dirty === false && lastWriterMessage(messages)?.loaded === true);

    other = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const closes = [];
    other.on("connection-close", (event) => {
      if (event) closes.push(event.reason);
    });
    await providerSynced(other);
    writer.sendMessage(JSON.stringify({ type: "file_reload", version: 9 }));
    await waitFor(() => closes.includes("file_reloaded"));
    assert.notEqual(lastWriterMessage(messages).generation, generation);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom moves the writer on release and tells the others the file changed outside", async () => {
  const runtime = createRuntime();
  let providerA;
  let providerB;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-release";
    const resource = { resource_id: "space-runtime:item-release:app-runtime" };
    providerA = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messagesA = customMessages(providerA);
    await providerSynced(providerA);
    await waitFor(() => lastWriterMessage(messagesA)?.writer === true);
    assert.equal(lastWriterMessage(messagesA).has_writer, true);

    providerB = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const messagesB = customMessages(providerB);
    await providerSynced(providerB);
    await waitFor(() => lastWriterMessage(messagesB) !== undefined);
    assert.equal(lastWriterMessage(messagesB).writer, false);
    assert.equal(lastWriterMessage(messagesB).has_writer, true);

    providerB.sendMessage(JSON.stringify({ type: "file_outside" }));
    providerB.sendMessage(JSON.stringify({ type: "file_writer_release" }));
    await pause(300);
    assert.equal(lastWriterMessage(messagesA).writer, true);
    assert.ok(!messagesA.some((message) => message.type === "file_outside"));

    providerA.sendMessage(JSON.stringify({ type: "file_outside" }));
    providerA.sendMessage(JSON.stringify({ type: "file_writer_release" }));
    await waitFor(() => messagesB.some((message) => message.type === "file_outside"));
    await waitFor(() => lastWriterMessage(messagesB)?.writer === true);
    await waitFor(() => lastWriterMessage(messagesA)?.writer === false);

    providerB.sendMessage(JSON.stringify({ type: "file_writer_release" }));
    await waitFor(() => lastWriterMessage(messagesB)?.writer === false);
    assert.equal(lastWriterMessage(messagesB).has_writer, false);
    assert.equal(lastWriterMessage(messagesA).writer, false);
  } finally {
    providerA?.destroy();
    providerB?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom moves its base for the save of the copy whose person wrote the content, and takes it as clean only when it matches the room", async () => {
  const drive = { version: 4, content: 4, down: false, asked: [] };
  const runtime = createRuntime(undefined, { outboundService: driveVersions(drive) });
  let providerA;
  let providerB;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-saved-elsewhere";
    const resource = { resource_id: "space-runtime:item-elsewhere:app-runtime" };
    const docA = new Y.Doc();
    providerA = providerFor(runtimeUrl, room, docA, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messagesA = customMessages(providerA);
    await providerSynced(providerA);
    await waitFor(() => lastWriterMessage(messagesA)?.writer === true);
    docA.transact(() => {
      docA.getMap("content").set("value", 1);
      docA.getMap("misty:file").set("loaded", 4);
    });
    providerA.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(docA), version: 4 }));
    await waitFor(() => lastWriterMessage(messagesA)?.loaded === true && lastWriterMessage(messagesA)?.dirty === false);

    const docB = new Y.Doc();
    providerB = providerFor(runtimeUrl, room, docB, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const messagesB = customMessages(providerB);
    await providerSynced(providerB);
    await waitFor(() => docB.getMap("content").get("value") === 1);
    docB.getMap("content").set("value", 2);
    await waitFor(() => lastWriterMessage(messagesB)?.dirty === true);

    drive.version = 6;
    drive.content = 6;
    drive.by = "user-b";
    providerB.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "not-this-room", version: 6 }));
    await waitFor(() => lastWriterMessage(messagesB)?.base === 6);
    assert.equal(lastWriterMessage(messagesB).dirty, true);

    providerB.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(docB), version: 5 }));
    await waitFor(() => lastWriterMessage(messagesB)?.dirty === false);
    assert.equal(lastWriterMessage(messagesB).base, 6);
    assert.equal(lastWriterMessage(messagesB).writer, false);
  } finally {
    providerA?.destroy();
    providerB?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom stops saving a text room once a copy says the file changed somewhere else", async () => {
  const saves = [];
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        saves.push(JSON.parse(await request.text()));
        return new Response(JSON.stringify({ version: saves.length + 3 }));
      }
      return new Response("{}");
    },
  });
  let provider;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-outside";
    const doc = new Y.Doc();
    provider = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file"), "file-room");
    const messages = customMessages(provider);
    await providerSynced(provider);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getText("text").insert(0, "hello");
      doc.getMap("misty:file").set("loaded", 3);
    });
    provider.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc) }));
    await waitFor(() => lastWriterMessage(messages)?.loaded === true && lastWriterMessage(messages)?.dirty === false);

    doc.getText("text").insert(5, " there");
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    provider.sendMessage(JSON.stringify({ type: "file_outside", version: 3 }));
    await pause(200);
    assert.notEqual(lastWriterMessage(messages).outside, true);
    provider.sendMessage(JSON.stringify({ type: "file_outside", version: 8 }));
    await waitFor(() => lastWriterMessage(messages)?.outside === true);

    provider.destroy();
    provider = undefined;
    await pause(2500);
    assert.ok(!saves.some((save) => save.text === "hello there" && !save.conflict_copy));
    assert.ok(saves.some((save) => save.text === "hello there" && save.conflict_copy === true));
  } finally {
    provider?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom reloads a dirty room once its writer kept the room's content beside the file", async () => {
  const runtime = createRuntime();
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-kept";
    const resource = { resource_id: "space-runtime:item-kept:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.dirty === false && lastWriterMessage(messages)?.loaded === true);

    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);
    doc.getMap("content").set("value", 2);
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    await waitFor(() => otherDoc.getMap("content").get("value") === 2);

    other.sendMessage(JSON.stringify({ type: "file_reload", version: 9, kept: true, fingerprint: fingerprint(otherDoc) }));
    writer.sendMessage(JSON.stringify({ type: "file_reload", version: 9, kept: true, fingerprint: "not-this-room" }));
    writer.sendMessage(JSON.stringify({ type: "file_reload", version: 9 }));
    await pause(500);
    assert.equal(lastWriterMessage(messages).generation, generation);
    assert.equal(doc.getMap("content").get("value"), 2);

    const keptFingerprint = fingerprint(doc);
    writer.sendMessage(JSON.stringify({ type: "file_reload", version: 9, kept: true, fingerprint: keptFingerprint }));
    await waitFor(() => lastWriterMessage(otherMessages)?.generation !== generation);
    const reloaded = lastWriterMessage(otherMessages);
    assert.equal(reloaded.kept, true);
    assert.equal(reloaded.fingerprint, keptFingerprint);
    assert.equal(reloaded.loaded, false);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom lets any editor reload a clean text room", async () => {
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        return new Response(JSON.stringify({ version: 3 }));
      }
      return new Response("{}");
    },
  });
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-reload";
    const resource = { resource_id: "space-runtime:item-text-reload" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getText("text").insert(0, "hello");
      doc.getMap("misty:file").set("loaded", 3);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc) }));
    await waitFor(() => lastWriterMessage(messages)?.dirty === false && lastWriterMessage(messages)?.loaded === true);

    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);
    await waitFor(() => lastWriterMessage(otherMessages)?.writer === false);
    other.sendMessage(JSON.stringify({ type: "file_reload", version: 7 }));
    await waitFor(() => lastWriterMessage(messages)?.generation !== generation);
    assert.equal(lastWriterMessage(messages).kept, false);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom stops a text room the drive refuses as stale, and keeps its text beside the file before it forgets it", async () => {
  const saves = [];
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        const save = JSON.parse(await request.text());
        saves.push(save);
        if (save.conflict_copy) return new Response(JSON.stringify({ item_id: "copy", version: 1, name: "a (conflicted copy).md" }));
        return new Response(JSON.stringify({ code: "file_changed", version: 8 }), { status: 409 });
      }
      return new Response("{}");
    },
  });
  let provider;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-stale";
    const doc = new Y.Doc();
    provider = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file"), "file-room");
    const messages = customMessages(provider);
    await providerSynced(provider);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getText("text").insert(0, "hello");
      doc.getMap("misty:file").set("loaded", 3);
    });
    await waitFor(() => lastWriterMessage(messages)?.outside === true, 15_000);
    assert.equal(lastWriterMessage(messages).drive_version, 8);
    assert.ok(!saves.some((save) => save.conflict_copy));

    doc.getText("text").insert(5, " there");
    provider.destroy();
    provider = undefined;
    await waitFor(() => saves.some((save) => save.conflict_copy && save.text === "hello there"));
  } finally {
    provider?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom pauses every copy while its writer keeps a dirty app room, then reloads naming the copy", async () => {
  const runtime = createRuntime();
  let writer;
  let other;
  let late;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-resolve";
    const resource = { resource_id: "space-runtime:item-resolve:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.dirty === false && lastWriterMessage(messages)?.loaded === true);

    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);
    doc.getMap("content").set("value", 2);
    await waitFor(() => otherDoc.getMap("content").get("value") === 2);

    other.sendMessage(JSON.stringify({ type: "file_resolve", version: 9 }));
    await waitFor(() => otherMessages.some((message) => message.type === "file_resolve_declined"));
    writer.sendMessage(JSON.stringify({ type: "file_resolve", version: 3 }));
    await waitFor(() => messages.some((message) => message.type === "file_resolve_declined" && message.version === 3));

    writer.sendMessage(JSON.stringify({ type: "file_resolve", version: 9 }));
    await waitFor(() => otherMessages.some((message) => message.type === "file_resolving" && message.version === 9));
    assert.ok(messages.some((message) => message.type === "file_resolving" && message.version === 9));

    otherDoc.getMap("content").set("value", 3);
    await pause(500);
    assert.equal(doc.getMap("content").get("value"), 2);

    const lateDoc = new Y.Doc();
    late = providerFor(runtimeUrl, room, lateDoc, mintTicket(room, "user-c", "file", "editor", resource), "file-room");
    const lateMessages = customMessages(late);
    await providerSynced(late);
    await waitFor(() => lastWriterMessage(lateMessages)?.resolving === 9);

    const kept = fingerprint(doc);
    other.sendMessage(JSON.stringify({ type: "file_kept", version: 8, kept_name: "wrong.bin", fingerprint: kept }));
    writer.sendMessage(JSON.stringify({ type: "file_kept", version: 9, kept_name: "a (conflicted copy).bin", fingerprint: kept }));
    await waitFor(() => lastWriterMessage(lateMessages)?.kept_name === "a (conflicted copy).bin");
    assert.equal(lastWriterMessage(lateMessages).kept_fingerprint, kept);
    assert.equal(lastWriterMessage(lateMessages).resolving, 9);
    writer.sendMessage(
      JSON.stringify({ type: "file_reload", version: 9, kept: true, fingerprint: kept, kept_name: "a (conflicted copy).bin" }),
    );
    await waitFor(() => lastWriterMessage(otherMessages)?.generation !== generation);
    const reloaded = lastWriterMessage(otherMessages);
    assert.equal(reloaded.kept, true);
    assert.equal(reloaded.fingerprint, kept);
    assert.equal(reloaded.kept_name, "a (conflicted copy).bin");
  } finally {
    writer?.destroy();
    other?.destroy();
    late?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom stops pausing when a save covers the version it was keeping the room for, and takes what it refused again", async () => {
  const runtime = createRuntime();
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-resolve-covered";
    const resource = { resource_id: "space-runtime:item-covered:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    doc.getMap("content").set("value", 2);
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    writer.sendMessage(JSON.stringify({ type: "file_resolve", version: 6 }));
    await waitFor(() => messages.some((message) => message.type === "file_resolving" && message.version === 6));
    doc.getMap("dropped").set("value", 3);
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 6 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 6);
    assert.equal(lastWriterMessage(messages).resolving, undefined);
    doc.getMap("content").set("value", 4);
    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    await providerSynced(other);
    await waitFor(() => otherDoc.getMap("content").get("value") === 4);
    assert.equal(otherDoc.getMap("dropped").get("value"), 3);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom keeps a text room's text beside the file itself when its writer asks, then reloads naming the copy", async () => {
  const saves = [];
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        const save = JSON.parse(await request.text());
        saves.push(save);
        if (save.conflict_copy) return new Response(JSON.stringify({ item_id: "copy", version: 1, name: "plan (conflicted copy).md" }));
        return new Response(JSON.stringify({ version: 4 }));
      }
      return new Response("{}");
    },
  });
  let writer;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-resolve";
    const resource = { resource_id: "space-runtime:item-text-resolve" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getText("text").insert(0, "hello");
      doc.getMap("misty:file").set("loaded", 3);
    });
    doc.getText("text").insert(5, " there");
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    writer.sendMessage(JSON.stringify({ type: "file_resolve", version: 9 }));
    await waitFor(() => lastWriterMessage(messages)?.generation !== generation, 15_000);
    const reloaded = lastWriterMessage(messages);
    assert.equal(reloaded.kept, true);
    assert.equal(reloaded.kept_name, "plan (conflicted copy).md");
    assert.ok(messages.some((message) => message.type === "file_resolving" && message.version === 9));
    assert.ok(saves.some((save) => save.conflict_copy && save.text === "hello there" && save.user_id === "user-a"));
  } finally {
    writer?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom keeps a stopped text room's text as another recent editor when the last one can't write", async () => {
  const saves = [];
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        const save = JSON.parse(await request.text());
        saves.push(save);
        if (!save.conflict_copy) return new Response(JSON.stringify({ code: "file_changed", version: 8 }), { status: 409 });
        if (save.user_id === "user-b") return new Response(JSON.stringify({ code: "forbidden" }), { status: 403 });
        return new Response(JSON.stringify({ item_id: "copy", version: 1, name: "a (conflicted copy).md" }));
      }
      return new Response("{}");
    },
  });
  let first;
  let second;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-editors";
    const firstDoc = new Y.Doc();
    first = providerFor(runtimeUrl, room, firstDoc, mintTicket(room, "user-a", "file"), "file-room");
    await providerSynced(first);
    const secondDoc = new Y.Doc();
    second = providerFor(runtimeUrl, room, secondDoc, mintTicket(room, "user-b", "file"), "file-room");
    const messages = customMessages(second);
    await providerSynced(second);
    first.destroy();
    first = undefined;
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    secondDoc.transact(() => {
      secondDoc.getText("text").insert(0, "hello");
      secondDoc.getMap("misty:file").set("loaded", 3);
    });
    await waitFor(() => lastWriterMessage(messages)?.outside === true, 15_000);
    secondDoc.getText("text").insert(5, " again");
    second.destroy();
    second = undefined;
    await waitFor(() => saves.some((save) => save.conflict_copy && save.user_id === "user-a" && save.text === "hello again"));
    assert.ok(saves.some((save) => save.conflict_copy && save.user_id === "user-b"));
  } finally {
    first?.destroy();
    second?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom counts a rename an editor saw as known, without raising its base or counting the room as saved", async () => {
  const runtime = createRuntime();
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-rename-base";
    const resource = { resource_id: "space-runtime:item-rename:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.loaded === true && lastWriterMessage(messages)?.dirty === false);
    await waitFor(() => lastWriterMessage(messages)?.base === 4);

    doc.getMap("content").set("value", 2);
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    other = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);
    other.sendMessage(JSON.stringify({ type: "file_saved", renamed: 6 }));
    await waitFor(() => lastWriterMessage(messages)?.renamed === 6);
    assert.equal(lastWriterMessage(messages).base, 4);
    assert.equal(lastWriterMessage(messages).dirty, true);
    await waitFor(() => lastWriterMessage(otherMessages)?.renamed === 6);
    writer.sendMessage(JSON.stringify({ type: "file_saved", version: 7 }));
    writer.sendMessage(JSON.stringify({ type: "file_resolve", version: 6 }));
    await waitFor(() => messages.some((message) => message.type === "file_resolve_declined" && message.version === 6));
    await pause(300);
    assert.equal(lastWriterMessage(messages).base, 4);
    assert.equal(lastWriterMessage(messages).renamed, 6);
    assert.equal(lastWriterMessage(messages).dirty, true);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom counts a text room's rename as known but saves from the content's version", async () => {
  const saves = [];
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) {
        saves.push(JSON.parse(await request.text()));
        return new Response(JSON.stringify({ version: 9 }));
      }
      return new Response("{}");
    },
  });
  let provider;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-rename";
    const doc = new Y.Doc();
    provider = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file"), "file-room");
    const messages = customMessages(provider);
    await providerSynced(provider);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getText("text").insert(0, "hello");
      doc.getMap("misty:file").set("loaded", 3);
    });
    provider.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc) }));
    await waitFor(() => lastWriterMessage(messages)?.loaded === true && lastWriterMessage(messages)?.dirty === false);

    doc.getText("text").insert(5, " there");
    await waitFor(() => lastWriterMessage(messages)?.dirty === true);
    provider.sendMessage(JSON.stringify({ type: "file_saved", renamed: 5 }));
    await waitFor(() => lastWriterMessage(messages)?.renamed === 5);
    assert.equal(lastWriterMessage(messages).base, 0);
    assert.equal(lastWriterMessage(messages).dirty, true);
    provider.sendMessage(JSON.stringify({ type: "file_outside", version: 5 }));
    await pause(200);
    assert.notEqual(lastWriterMessage(messages).outside, true);

    provider.destroy();
    provider = undefined;
    await waitFor(() => saves.some((save) => save.text === "hello there"));
    assert.equal(saves.find((save) => save.text === "hello there").base_version, 3);
  } finally {
    provider?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom ignores heuristic renames once a host reports content versions, so they can't block its reload", async () => {
  const runtime = createRuntime();
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-content-mode";
    const resource = { resource_id: "space-runtime:item-content-mode:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.dirty === false && lastWriterMessage(messages)?.loaded === true);
    writer.sendMessage(JSON.stringify({ type: "file_mode", content: true }));
    await pause(200);

    other = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const closes = [];
    other.on("connection-close", (event) => {
      if (event) closes.push(event.reason);
    });
    await providerSynced(other);
    other.sendMessage(JSON.stringify({ type: "file_saved", renamed: 6 }));
    await pause(400);
    assert.equal(lastWriterMessage(messages).renamed, undefined);
    writer.sendMessage(JSON.stringify({ type: "file_reload", version: 6 }));
    await waitFor(() => closes.includes("file_reloaded"));
    assert.notEqual(lastWriterMessage(messages).generation, generation);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom raises its base only to a version the drive confirms, and trusts only its writer while the drive can't answer", async () => {
  const drive = { version: 4, content: 4, down: false, asked: [] };
  const runtime = createRuntime(undefined, { outboundService: driveVersions(drive) });
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-verified-base";
    const resource = { resource_id: "space-runtime:item-verified:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 4 && lastWriterMessage(messages)?.dirty === false);
    assert.deepEqual(drive.asked.at(-1), { space_id: "space-runtime", item_id: "item-verified", user_id: "user-a" });

    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 9 }));
    await pause(400);
    assert.equal(lastWriterMessage(messages).base, 4);

    drive.version = 5;
    drive.content = 5;
    drive.by = "user-b";
    other = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);
    other.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 7 }));
    await pause(400);
    assert.equal(lastWriterMessage(otherMessages).base, 4);
    other.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 5 }));
    await waitFor(() => lastWriterMessage(otherMessages)?.base === 5);
    assert.equal(drive.asked.at(-1).user_id, "user-b");

    drive.down = true;
    drive.version = 8;
    drive.content = 8;
    other.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 8 }));
    await pause(400);
    assert.equal(lastWriterMessage(messages).base, 5);
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 8 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 8);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom raises its base for a copy's word only from whoever wrote the drive's content, and after a rename only from its writer", async () => {
  const drive = { version: 4, content: 4, by: "user-a", down: false, asked: [] };
  const runtime = createRuntime(undefined, { outboundService: driveVersions(drive) });
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-verified-claimant";
    const resource = { resource_id: "space-runtime:item-claimant:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 4 && lastWriterMessage(messages)?.dirty === false);

    other = providerFor(runtimeUrl, room, new Y.Doc(), mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);

    drive.version = 5;
    drive.content = 5;
    drive.by = "user-c";
    const asked = drive.asked.length;
    other.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 5 }));
    await waitFor(() => drive.asked.length === asked + 1);
    await pause(400);
    assert.equal(lastWriterMessage(messages).base, 4);
    assert.equal(drive.asked.at(-1).user_id, "user-b");

    drive.by = "user-b";
    other.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 5 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 5);
    assert.equal(lastWriterMessage(otherMessages).base, 5);

    drive.version = 7;
    drive.content = 6;
    const renamedAsked = drive.asked.length;
    other.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: "elsewhere", version: 6 }));
    await waitFor(() => drive.asked.length === renamedAsked + 1);
    await pause(400);
    assert.equal(lastWriterMessage(messages).base, 5);
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 6 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 6);
    assert.equal(drive.asked.at(-1).user_id, "user-a");
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom takes a kept copy and reloads for it only once the drive has reached that version", async () => {
  const drive = { version: 4, content: 4, down: false, asked: [] };
  const runtime = createRuntime(undefined, { outboundService: driveVersions(drive) });
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-verified-kept";
    const resource = { resource_id: "space-runtime:item-verified-kept:app-runtime" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    const generation = lastWriterMessage(messages).generation;
    doc.transact(() => {
      doc.getMap("content").set("value", 1);
      doc.getMap("misty:file").set("loaded", 4);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc), version: 4 }));
    await waitFor(() => lastWriterMessage(messages)?.base === 4 && lastWriterMessage(messages)?.dirty === false);

    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    const otherMessages = customMessages(other);
    await providerSynced(other);
    doc.getMap("content").set("value", 2);
    await waitFor(() => otherDoc.getMap("content").get("value") === 2);
    writer.sendMessage(JSON.stringify({ type: "file_resolve", version: 9 }));
    await waitFor(() => otherMessages.some((message) => message.type === "file_resolving" && message.version === 9));

    const kept = fingerprint(doc);
    const keptMessage = { type: "file_kept", version: 9, kept_name: "a (conflicted copy).bin", fingerprint: kept };
    const reload = { type: "file_reload", version: 9, kept: true, fingerprint: kept, kept_name: "a (conflicted copy).bin" };
    other.sendMessage(JSON.stringify({ ...keptMessage, kept_name: "fake.bin" }));
    writer.sendMessage(JSON.stringify(keptMessage));
    writer.sendMessage(JSON.stringify(reload));
    await pause(500);
    assert.equal(lastWriterMessage(otherMessages).kept_name, undefined);
    assert.equal(lastWriterMessage(otherMessages).generation, generation);

    drive.version = 9;
    drive.content = 9;
    writer.sendMessage(JSON.stringify(keptMessage));
    await waitFor(() => lastWriterMessage(otherMessages)?.kept_name === "a (conflicted copy).bin");
    writer.sendMessage(JSON.stringify(reload));
    await waitFor(() => lastWriterMessage(otherMessages)?.generation !== generation);
    assert.equal(lastWriterMessage(otherMessages).kept_name, "a (conflicted copy).bin");
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});

test("FileRoom stops a text room's saves for a copy's word only once the drive shows a real change past its save", async () => {
  const drive = { version: 3, content: 3, down: false, asked: [] };
  const versions = driveVersions(drive);
  const runtime = createRuntime(undefined, {
    outboundService: async (request) => {
      if (request.url.endsWith("/internal/journal/file-texts")) return new Response(JSON.stringify({ version: 3 }));
      return versions(request);
    },
  });
  let writer;
  let other;
  try {
    const runtimeUrl = await runtime.ready;
    const room = "runtime-file-text-verified-outside";
    const resource = { resource_id: "space-runtime:item-text-outside" };
    const doc = new Y.Doc();
    writer = providerFor(runtimeUrl, room, doc, mintTicket(room, "user-a", "file", "editor", resource), "file-room");
    const messages = customMessages(writer);
    await providerSynced(writer);
    await waitFor(() => lastWriterMessage(messages)?.writer === true);
    doc.transact(() => {
      doc.getText("text").insert(0, "hello");
      doc.getMap("misty:file").set("loaded", 3);
    });
    writer.sendMessage(JSON.stringify({ type: "file_saved", fingerprint: fingerprint(doc) }));
    await waitFor(() => lastWriterMessage(messages)?.loaded === true && lastWriterMessage(messages)?.dirty === false);

    const otherDoc = new Y.Doc();
    other = providerFor(runtimeUrl, room, otherDoc, mintTicket(room, "user-b", "file", "editor", resource), "file-room");
    await providerSynced(other);
    await waitFor(() => otherDoc.getText("text").toString() === "hello");

    other.sendMessage(JSON.stringify({ type: "file_outside", version: 8 }));
    await waitFor(() => drive.asked.length === 1);
    await pause(200);
    assert.notEqual(lastWriterMessage(messages).outside, true);
    assert.deepEqual(drive.asked[0], { space_id: "space-runtime", item_id: "item-text-outside", user_id: "user-b" });

    drive.down = true;
    other.sendMessage(JSON.stringify({ type: "file_outside", version: 8 }));
    await waitFor(() => drive.asked.length === 2);
    await pause(200);
    assert.notEqual(lastWriterMessage(messages).outside, true);

    drive.down = false;
    drive.version = 8;
    other.sendMessage(JSON.stringify({ type: "file_outside", version: 8 }));
    await waitFor(() => drive.asked.length === 3);
    await pause(200);
    assert.notEqual(lastWriterMessage(messages).outside, true);

    drive.content = 8;
    other.sendMessage(JSON.stringify({ type: "file_outside", version: 8 }));
    await waitFor(() => lastWriterMessage(messages)?.outside === true);
    assert.equal(lastWriterMessage(messages).drive_version, 8);
  } finally {
    writer?.destroy();
    other?.destroy();
    await runtime.dispose();
  }
});
