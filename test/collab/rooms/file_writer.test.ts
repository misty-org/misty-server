import { describe, expect, test } from "vitest";
import { closedWith, textIs } from "../harness/helpers.js";
import { fileItem, fileRoom, heard, loadApp, loadText, writerOf, writerSays } from "../harness/files.js";
import { CLOSE_CODES, VIAS } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { fingerprint, onDoc, pause, until } from "../harness/wait.js";
import { driveUntil, sha256 } from "../harness/world.js";

describe.each(VIAS)("file rooms: the writer, saves and reloads over %s", (via) => {
  const s = suite(via, "File writer");

  test("a text room saves a delete-only edit to the drive when its last editor leaves", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "delete-only", "text", 3);
    const a = await fileRoom(s, ada, item, "text");
    await writerSays(a, (said) => said.writer === true, "the writer");
    expect(writerOf(a)).toMatchObject({ loaded: false, read_only: false });
    expect(typeof writerOf(a)?.generation).toBe("string");
    loadText(a, "hello world", 3);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc) });
    await writerSays(a, (said) => said.loaded === true, "loaded");
    expect(writerOf(a)?.dirty).toBe(false);
    a.doc.getText("text").delete(0, 6);
    await writerSays(a, (said) => said.dirty === true, "dirty");
    a.close();
    await driveUntil(async () => (await s.world.item(item.id)).sha256 === sha256("world"), 20_000, "the drive to hold world");
    const saved = await s.world.item(item.id);
    expect(saved.version).toBe(4);
    expect(saved.size).toBe(5);
    expect(saved.modified_by).toBe(ada.id);
  });

  test("the writer is handed over when it leaves before loading, in the same generation", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "handover", "app");
    const a = await fileRoom(s, ada, item, "app");
    await writerSays(a, (said) => said.writer === true, "the first writer");
    const b = await fileRoom(s, ben, item, "app");
    await writerSays(b, () => true, "the second's state");
    expect(writerOf(b)?.writer).toBe(false);
    const generation = writerOf(b)?.generation;
    a.close();
    await writerSays(b, (said) => said.writer === true, "the hand-over");
    expect(writerOf(b)).toMatchObject({ loaded: false, generation });
    b.doc.getMap("misty:file").set("loaded", 1);
    await writerSays(b, (said) => said.loaded === true, "loaded");
    expect(writerOf(b)?.dirty).toBe(true);
    b.send({ type: "file_saved", fingerprint: fingerprint(b.doc) });
    await writerSays(b, (said) => said.dirty === false, "clean");
  });

  test("the writer moves to the same person's newer connection", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "same-person", "app");
    const older = await fileRoom(s, ada, item, "app");
    await writerSays(older, (said) => said.writer === true, "the older writer");
    const newer = await fileRoom(s, ada, item, "app");
    await writerSays(newer, (said) => said.writer === true, "the newer writer");
    await writerSays(older, (said) => said.writer === false, "the older to stop writing");
  });

  test("a clean room reloads from the drive when its writer asks, a dirty one never does, and every connection moves", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "reload", "app", 4);
    const a = await fileRoom(s, ada, item, "app");
    await writerSays(a, (said) => said.writer === true, "the writer");
    const generation = writerOf(a)?.generation;
    loadApp(a, 1, 4);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc), version: 4 });
    await writerSays(a, (said) => said.loaded === true && said.dirty === false && said.base === 4, "clean at 4");

    a.doc.getMap("content").set("value", 2);
    await writerSays(a, (said) => said.dirty === true, "dirty");
    a.send({ type: "file_reload", version: 7 });
    await pause(500);
    expect(writerOf(a)?.generation).toBe(generation);
    expect(a.doc.getMap("content").get("value")).toBe(2);

    await s.world.setVersions(item.id, 5, 5, ada);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc), version: 5 });
    await writerSays(a, (said) => said.dirty === false && said.base === 5, "clean at 5");
    a.send({ type: "file_reload", version: 5 });
    await pause(500);
    expect(writerOf(a)?.generation).toBe(generation);

    a.send({ type: "file_reload", version: 7 });
    await writerSays(a, (said) => said.generation !== generation, "the new generation");
    const reloaded = writerOf(a);
    expect(reloaded).toMatchObject({ loaded: false, has_writer: false, kept: false });
    await until(() => a.doc.getMap("content").get("value") === undefined, [onDoc(a.doc)], 10_000, "the room to empty");
    expect(a.doc.getMap("misty:file").get("loaded")).toBeUndefined();
    await closedWith(a, CLOSE_CODES.moved);
    await until(() => a.opens.items.length === 2, [a.opens.source], 10_000, "the rejoin");
    expect(a.opens.items[1]).toBe(s.instance.url);
    a.close();

    const fresh = await fileRoom(s, ada, item, "app");
    await writerSays(fresh, (said) => said.writer === true, "the fresh writer");
    expect(writerOf(fresh)).toMatchObject({ loaded: false, has_writer: true });
    expect(fresh.doc.getMap("content").get("value")).toBeUndefined();
    expect(fresh.doc.getMap("misty:file").get("loaded")).toBeUndefined();
  });

  test("a reload closes every connection of the old generation with moved, to the same instance", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "reload-closes", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.dirty === false && said.loaded === true, "clean");
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    writer.send({ type: "file_reload", version: 9 });
    await closedWith(other, CLOSE_CODES.moved);
    await closedWith(writer, CLOSE_CODES.moved);
    expect(writerOf(writer)?.generation).not.toBe(generation);
    expect(other.frames.some((type) => type === "moved")).toBe(true);
    await until(() => other.opens.items.length === 2 && writer.opens.items.length === 2, [other.opens.source, writer.opens.source], 10_000, "both to rejoin");
    expect(other.opens.items[1]).toBe(s.instance.url);
    expect(writer.opens.items[1]).toBe(s.instance.url);
  });

  test("a text room reloaded from the drive with two people in it comes back with the drive's text once", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "reload-two", "text", 3);
    const a = await fileRoom(s, ada, item, "text");
    await writerSays(a, (said) => said.writer === true, "the writer");
    loadText(a, "hello", 3);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc) });
    await writerSays(a, (said) => said.loaded === true && said.dirty === false, "clean");
    const b = await fileRoom(s, ben, item, "text");
    await textIs(b, "text", "hello");
    const generation = writerOf(a)?.generation;
    const changed = await s.world.write(item.id, "hello from the drive", ben);
    b.send({ type: "file_reload", version: changed.version });
    await closedWith(a, CLOSE_CODES.moved);
    await closedWith(b, CLOSE_CODES.moved);
    await until(() => a.opens.items.length === 2 && b.opens.items.length === 2, [a.opens.source, b.opens.source], 10_000, "both to rejoin");
    expect([a.opens.items[1], b.opens.items[1]]).toEqual([s.instance.url, s.instance.url]);
    await until(
      () => [a, b].some((each) => writerOf(each)?.writer === true && writerOf(each)?.generation !== generation),
      [a.file.source, b.file.source],
      10_000,
      "a writer in the new generation",
    );
    const writer = writerOf(a)?.writer === true ? a : b;
    expect(writer.doc.getText("text").toString()).toBe("");
    loadText(writer, "hello from the drive", changed.version);
    await textIs(a, "text", "hello from the drive");
    await textIs(b, "text", "hello from the drive");
    await pause(500);
    expect(a.doc.getText("text").toString()).toBe("hello from the drive");
    expect(b.doc.getText("text").toString()).toBe("hello from the drive");
  });

  test("the writer moves on release, and only the writer tells the others the file changed outside", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "release", "app");
    const a = await fileRoom(s, ada, item, "app");
    await writerSays(a, (said) => said.writer === true && said.has_writer === true, "the writer");
    const b = await fileRoom(s, ben, item, "app");
    await writerSays(b, () => true, "the other's state");
    expect(writerOf(b)).toMatchObject({ writer: false, has_writer: true });
    b.send({ type: "file_outside" });
    b.send({ type: "file_writer_release" });
    await pause(300);
    expect(writerOf(a)?.writer).toBe(true);
    expect(a.file.some((message) => message.type === "file_outside")).toBe(false);
    a.send({ type: "file_outside" });
    a.send({ type: "file_writer_release" });
    await heard(b, "file_outside");
    await writerSays(b, (said) => said.writer === true, "the hand-over");
    await writerSays(a, (said) => said.writer === false, "the release");
    b.send({ type: "file_writer_release" });
    await writerSays(b, (said) => said.writer === false, "the second release");
    expect(writerOf(b)?.has_writer).toBe(false);
    expect(writerOf(a)?.writer).toBe(false);
  });

  test("the base moves for a save by the person who wrote the drive's content, and the room is clean only when it matches", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "saved-elsewhere", "app", 4);
    const a = await fileRoom(s, ada, item, "app");
    await writerSays(a, (said) => said.writer === true, "the writer");
    loadApp(a, 1, 4);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc), version: 4 });
    await writerSays(a, (said) => said.loaded === true && said.dirty === false, "clean");
    const b = await fileRoom(s, ben, item, "app");
    await until(() => b.doc.getMap("content").get("value") === 1, [onDoc(b.doc)], 10_000, "the content");
    b.doc.getMap("content").set("value", 2);
    await writerSays(b, (said) => said.dirty === true, "dirty");
    await s.world.setVersions(item.id, 6, 6, ben);
    b.send({ type: "file_saved", fingerprint: "not-this-room", version: 6 });
    await writerSays(b, (said) => said.base === 6, "the base at 6");
    expect(writerOf(b)?.dirty).toBe(true);
    b.send({ type: "file_saved", fingerprint: fingerprint(b.doc), version: 5 });
    await writerSays(b, (said) => said.dirty === false, "clean");
    expect(writerOf(b)).toMatchObject({ base: 6, writer: false });
  });

  test("a dirty room reloads once its writer kept the room's content beside the file, and only then", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "kept", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.dirty === false && said.loaded === true, "clean");
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    writer.doc.getMap("content").set("value", 2);
    await writerSays(writer, (said) => said.dirty === true, "dirty");
    await until(() => other.doc.getMap("content").get("value") === 2, [onDoc(other.doc)], 10_000, "the edit");
    await s.world.setVersions(item.id, 9, 9, ada);
    other.send({ type: "file_reload", version: 9, kept: true, fingerprint: fingerprint(other.doc) });
    writer.send({ type: "file_reload", version: 9, kept: true, fingerprint: "not-this-room" });
    writer.send({ type: "file_reload", version: 9 });
    await pause(500);
    expect(writerOf(writer)?.generation).toBe(generation);
    expect(writer.doc.getMap("content").get("value")).toBe(2);
    const kept = fingerprint(writer.doc);
    writer.send({ type: "file_reload", version: 9, kept: true, fingerprint: kept });
    await writerSays(other, (said) => said.generation !== generation, "the new generation");
    expect(writerOf(other)).toMatchObject({ kept: true, fingerprint: kept, loaded: false });
  });

  test("any editor may reload a clean text room", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "text-reload", "text", 3);
    const writer = await fileRoom(s, ada, item, "text");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadText(writer, "hello", 3);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc) });
    await writerSays(writer, (said) => said.dirty === false && said.loaded === true, "clean");
    const other = await fileRoom(s, ben, item, "text");
    await writerSays(other, (said) => said.writer === false, "the other");
    other.send({ type: "file_reload", version: 7 });
    await writerSays(writer, (said) => said.generation !== generation, "the new generation");
    expect(writerOf(writer)?.kept).toBe(false);
  });

  test("a rename an editor saw counts as known without raising the base or making the room clean", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "rename-base", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.loaded === true && said.dirty === false && said.base === 4, "clean at 4");
    writer.doc.getMap("content").set("value", 2);
    await writerSays(writer, (said) => said.dirty === true, "dirty");
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    other.send({ type: "file_saved", renamed: 6 });
    await writerSays(writer, (said) => said.renamed === 6, "the rename");
    expect(writerOf(writer)).toMatchObject({ base: 4, dirty: true });
    await writerSays(other, (said) => said.renamed === 6, "the rename for the other");
    writer.send({ type: "file_saved", version: 7 });
    writer.send({ type: "file_resolve", version: 6 });
    await heard(writer, "file_resolve_declined", (message) => message.version === 6);
    await pause(300);
    expect(writerOf(writer)).toMatchObject({ base: 4, renamed: 6, dirty: true });
  });

  test("once a host reports content versions, heuristic renames are ignored and can't block its reload", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "content-mode", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.dirty === false && said.loaded === true, "clean");
    writer.send({ type: "file_mode", content: true });
    await pause(200);
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    other.send({ type: "file_saved", renamed: 6 });
    await pause(400);
    expect(writerOf(writer)?.renamed).toBeUndefined();
    writer.send({ type: "file_reload", version: 6 });
    await closedWith(other, CLOSE_CODES.moved);
    expect(writerOf(writer)?.generation).not.toBe(generation);
  });
});
