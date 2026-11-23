import * as encoding from "lib0/encoding";
import { describe, expect, it } from "vitest";
import * as Y from "yjs";

import { DOCUMENT_WARNING_BYTES, MAX_DOCUMENT_BYTES } from "../src/document_persistence";
import { DocumentSizeGate } from "../src/room_message_gate";

function syncUpdateMessage(update: Uint8Array): ArrayBuffer {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, 0);
  encoding.writeVarUint(encoder, 2);
  encoding.writeVarUint8Array(encoder, update);
  return encoding.toUint8Array(encoder).slice().buffer;
}

function insertion(base: Y.Doc, text: string): Uint8Array {
  const incoming = new Y.Doc();
  Y.applyUpdate(incoming, Y.encodeStateAsUpdate(base));
  const vector = Y.encodeStateVector(incoming);
  incoming.getText("content").insert(0, text);
  return Y.encodeStateAsUpdate(incoming, vector);
}

describe("document size gate", () => {
  it("lets small updates through without measuring the document", () => {
    const document = new Y.Doc();
    document.getText("content").insert(0, "saved");
    const gate = new DocumentSizeGate(document);
    gate.measure();

    expect(gate.check(syncUpdateMessage(insertion(document, "more")))).toBeNull();
  });

  it("measures again instead of trusting a stale estimate near the ceiling", () => {
    const document = new Y.Doc();
    document.getText("content").insert(0, "saved");
    const gate = new DocumentSizeGate(document);
    gate.measured(MAX_DOCUMENT_BYTES);

    expect(gate.check(syncUpdateMessage(insertion(document, "more")))).toBeNull();
  });

  it("warns near the ceiling and blocks past it", () => {
    const document = new Y.Doc();
    document.getText("content").insert(0, "x".repeat(DOCUMENT_WARNING_BYTES + 1024));
    const gate = new DocumentSizeGate(document);
    gate.measure();

    expect(gate.check(syncUpdateMessage(insertion(document, "y")))?.status).toBe("warning");
    const tooMuch = "z".repeat(MAX_DOCUMENT_BYTES - DOCUMENT_WARNING_BYTES);
    expect(gate.check(syncUpdateMessage(insertion(document, tooMuch)))?.status).toBe("blocked");
  });

  it("counts updates applied since the last measurement", () => {
    const document = new Y.Doc();
    const gate = new DocumentSizeGate(document);
    gate.measure();
    document.getText("content").insert(0, "x".repeat(DOCUMENT_WARNING_BYTES + 1024));

    expect(gate.check(syncUpdateMessage(insertion(document, "y")))?.status).toBe("warning");
  });

  it("ignores messages that are not document updates", () => {
    const gate = new DocumentSizeGate(new Y.Doc());
    expect(gate.check(new Uint8Array([0, 0, 1, 0]).buffer)).toBeNull();
    expect(gate.check(new Uint8Array([1, 0]).buffer)).toBeNull();
  });
});
