import { describe, expect, test } from "vitest";
import { closedWith, saved, textIs } from "../harness/helpers.js";
import { fileItem, fileRoom, heard, loadApp, loadText, writerOf, writerSays } from "../harness/files.js";
import { CLOSE_CODES, VIAS } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { fingerprint, onDoc, pause, until } from "../harness/wait.js";
import { call, driveUntil, sha256, world } from "../harness/world.js";

describe.each(VIAS)("file rooms: outside changes, copies and versions over %s", (via) => {
  const s = suite(via, "File conflicts");

  const copyHolding = async (item: Parameters<typeof s.world.copies>[0], text: string) =>
    (await s.world.copies(item)).find((copy) => copy.sha256 === sha256(text));

  test("a text room stops saving once a copy says the file changed elsewhere, and keeps its text beside the file instead", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "text-outside", "text", 3);
    const a = await fileRoom(s, ada, item, "text");
    await writerSays(a, (said) => said.writer === true, "the writer");
    loadText(a, "hello", 3);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc) });
    await writerSays(a, (said) => said.loaded === true && said.dirty === false, "clean");
    a.doc.getText("text").insert(5, " there");
    await writerSays(a, (said) => said.dirty === true, "dirty");
    a.send({ type: "file_outside", version: 3 });
    await pause(200);
    expect(writerOf(a)?.outside).not.toBe(true);
    await s.world.setVersions(item.id, 8, 8, ada);
    a.send({ type: "file_outside", version: 8 });
    await writerSays(a, (said) => said.outside === true, "outside");
    a.close();
    await driveUntil(async () => (await copyHolding(item, "hello there")) !== undefined, 20_000, "the kept copy");
    expect((await copyHolding(item, "hello there"))?.modified_by).toBe(ada.id);
    expect((await s.world.item(item.id)).sha256).toBe(sha256(""));
  });

  test("a text room the drive refuses as stale stops, and keeps its text beside the file before it forgets it", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "text-stale", "text", 8);
    const a = await fileRoom(s, ada, item, "text");
    await writerSays(a, (said) => said.writer === true, "the writer");
    loadText(a, "hello", 3);
    await writerSays(a, (said) => said.outside === true, "the stale save", 15_000);
    expect(writerOf(a)?.drive_version).toBe(8);
    expect(await s.world.copies(item)).toEqual([]);
    a.doc.getText("text").insert(5, " there");
    a.close();
    await driveUntil(async () => (await copyHolding(item, "hello there")) !== undefined, 20_000, "the kept copy");
    expect((await s.world.item(item.id)).sha256).toBe(sha256(""));
  });

  test("while its writer keeps a dirty app room, every copy pauses, then the room reloads naming the copy", async () => {
    const { ada, ben, cyd } = s.world.people;
    const item = await fileItem(s, "resolve", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.dirty === false && said.loaded === true, "clean");
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    writer.doc.getMap("content").set("value", 2);
    await until(() => other.doc.getMap("content").get("value") === 2, [onDoc(other.doc)], 10_000, "the edit");

    other.send({ type: "file_resolve", version: 9 });
    await heard(other, "file_resolve_declined");
    writer.send({ type: "file_resolve", version: 3 });
    await heard(writer, "file_resolve_declined", (message) => message.version === 3);
    writer.send({ type: "file_resolve", version: 9 });
    await heard(other, "file_resolving", (message) => message.version === 9);
    await heard(writer, "file_resolving", (message) => message.version === 9);

    other.doc.getMap("content").set("value", 3);
    await pause(500);
    expect(writer.doc.getMap("content").get("value")).toBe(2);
    const late = await fileRoom(s, cyd, item, "app");
    await writerSays(late, (said) => said.resolving === 9, "the late joiner to pause");

    await s.world.setVersions(item.id, 9, 9, ben);
    const kept = fingerprint(writer.doc);
    other.send({ type: "file_kept", version: 8, kept_name: "wrong.bin", fingerprint: kept });
    writer.send({ type: "file_kept", version: 9, kept_name: "a (conflicted copy).bin", fingerprint: kept });
    await writerSays(late, (said) => said.kept_name === "a (conflicted copy).bin", "the kept name");
    expect(writerOf(late)).toMatchObject({ kept_fingerprint: kept, resolving: 9 });
    writer.send({ type: "file_reload", version: 9, kept: true, fingerprint: kept, kept_name: "a (conflicted copy).bin" });
    await writerSays(other, (said) => said.generation !== generation, "the new generation");
    expect(writerOf(other)).toMatchObject({ kept: true, fingerprint: kept, kept_name: "a (conflicted copy).bin" });
    await closedWith(late, CLOSE_CODES.moved);
  });

  test("a pause ends when a save covers the version it was for, and what it refused comes in again", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "resolve-covered", "app");
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    loadApp(writer, 1, 4);
    writer.doc.getMap("content").set("value", 2);
    await writerSays(writer, (said) => said.dirty === true, "dirty");
    writer.send({ type: "file_resolve", version: 6 });
    await heard(writer, "file_resolving", (message) => message.version === 6);
    writer.doc.getMap("dropped").set("value", 3);
    await s.world.setVersions(item.id, 6, 6, ada);
    writer.send({ type: "file_saved", fingerprint: "elsewhere", version: 6 });
    await writerSays(writer, (said) => said.base === 6, "the base at 6");
    expect(writerOf(writer)?.resolving).toBeUndefined();
    writer.doc.getMap("content").set("value", 4);
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    await until(() => other.doc.getMap("content").get("value") === 4, [onDoc(other.doc)], 10_000, "the later edit");
    await until(() => other.doc.getMap("dropped").get("value") === 3, [onDoc(other.doc)], 10_000, "the refused edit");
  });

  test("a text room's writer can keep its text beside the file, and the room reloads naming the copy", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "text-resolve", "text", 3);
    const writer = await fileRoom(s, ada, item, "text");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadText(writer, "hello", 3);
    writer.doc.getText("text").insert(5, " there");
    await writerSays(writer, (said) => said.dirty === true, "dirty");
    writer.send({ type: "file_resolve", version: 9 });
    await writerSays(writer, (said) => said.generation !== generation, "the new generation", 15_000);
    const reloaded = writerOf(writer);
    expect(reloaded?.kept).toBe(true);
    expect(reloaded?.kept_name).toMatch(/conflicted copy/);
    expect(writer.file.some((message) => message.type === "file_resolving" && message.version === 9)).toBe(true);
    const copy = await copyHolding(item, "hello there");
    expect(copy?.name).toBe(reloaded?.kept_name);
    expect(copy?.modified_by).toBe(ada.id);
  });

  test("a resolved text room with two people in it reloads them both to the same instance with the drive's text once", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "resolve-two", "text", 3);
    const a = await fileRoom(s, ada, item, "text");
    await writerSays(a, (said) => said.writer === true, "the writer");
    const generation = writerOf(a)?.generation;
    loadText(a, "hello", 3);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc) });
    await writerSays(a, (said) => said.dirty === false && said.loaded === true, "clean");
    const b = await fileRoom(s, ben, item, "text");
    await textIs(b, "text", "hello");
    const changed = await s.world.write(item.id, "the drive's own text", ben);
    a.doc.getText("text").insert(5, " mine");
    await writerSays(a, (said) => said.dirty === true, "dirty");
    a.send({ type: "file_resolve", version: changed.version });
    await heard(b, "file_resolving", (message) => message.version === changed.version);
    await closedWith(a, CLOSE_CODES.moved, 20_000);
    await closedWith(b, CLOSE_CODES.moved, 20_000);
    const keptAt = b.file.items.findIndex((message) => message.type === "file_writer" && message.kept === true);
    const kept = b.file.items[keptAt];
    expect(kept).toMatchObject({ kept: true, writer: false, has_writer: false });
    expect(b.file.items.slice(keptAt + 1, b.fileAtClose[0])).toEqual([]);
    expect((await copyHolding(item, "hello mine"))?.name).toBe(kept?.kept_name);
    await until(() => a.opens.items.length === 2 && b.opens.items.length === 2, [a.opens.source, b.opens.source], 10_000, "both to rejoin");
    expect([a.opens.items[1], b.opens.items[1]]).toEqual([s.instance.url, s.instance.url]);
    await until(
      () => [a, b].some((each) => writerOf(each)?.writer === true && writerOf(each)?.generation !== generation),
      [a.file.source, b.file.source],
      10_000,
      "a writer in the new generation",
    );
    const writer = writerOf(a)?.writer === true ? a : b;
    expect(a.doc.getText("text").toString()).toBe("");
    expect(b.doc.getText("text").toString()).toBe("");
    loadText(writer, "the drive's own text", changed.version);
    await textIs(a, "text", "the drive's own text");
    await textIs(b, "text", "the drive's own text");
    await pause(500);
    expect(a.doc.getText("text").toString()).toBe("the drive's own text");
    expect(b.doc.getText("text").toString()).toBe("the drive's own text");
  });

  test("a stopped text room keeps its text as another recent editor when the last one can't write any more", async () => {
    const own = await world("Editors");
    const { ada, ben } = own.people;
    const item = await own.file(`${s.key("editors")}.md`);
    await own.setVersions(item.id, 8, 8, ada);
    const options = { space: own.space };
    const first = await fileRoom(s, ada, item, "text", { ...options, token: (await own.token(ada)).token });
    await first.room.synced;
    const second = await fileRoom(s, ben, item, "text", { ...options, token: (await own.token(ben)).token });
    await second.room.synced;
    first.close();
    await writerSays(second, (said) => said.writer === true, "the hand-over");
    loadText(second, "hello", 3);
    await writerSays(second, (said) => said.outside === true, "the stale save", 15_000);
    await saved(second, () => second.doc.getText("text").insert(5, " again"));
    await call("/roles/everyone", { owner: ada.id, space: own.space, drive_write: false });
    await closedWith(second, CLOSE_CODES.access_changed);
    await driveUntil(async () => (await own.copies(item)).some((copy) => copy.sha256 === sha256("hello again")), 20_000, "the kept copy");
    const copy = (await own.copies(item)).find((each) => each.sha256 === sha256("hello again"));
    expect(copy?.modified_by).toBe(ada.id);
  });

  test("a text room counts its rename as known and still saves its text", async () => {
    const { ada } = s.world.people;
    const item = await fileItem(s, "text-rename", "text", 3);
    const a = await fileRoom(s, ada, item, "text");
    await writerSays(a, (said) => said.writer === true, "the writer");
    loadText(a, "hello", 3);
    a.send({ type: "file_saved", fingerprint: fingerprint(a.doc) });
    await writerSays(a, (said) => said.loaded === true && said.dirty === false, "clean");
    await s.world.setVersions(item.id, 5, 3);
    a.doc.getText("text").insert(5, " there");
    a.send({ type: "file_saved", renamed: 5 });
    await writerSays(a, (said) => said.renamed === 5, "the rename");
    expect(writerOf(a)?.base).toBe(0);
    a.send({ type: "file_outside", version: 5 });
    await pause(200);
    expect(writerOf(a)?.outside).not.toBe(true);
    a.close();
    await driveUntil(async () => (await s.world.item(item.id)).sha256 === sha256("hello there"), 20_000, "the saved text");
    expect((await s.world.item(item.id)).version).toBe(6);
  });
});
