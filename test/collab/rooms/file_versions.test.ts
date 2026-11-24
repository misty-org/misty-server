import { describe, expect, test } from "vitest";
import { textIs } from "../harness/helpers.js";
import { fileItem, fileRoom, heard, loadApp, loadText, writerOf, writerSays } from "../harness/files.js";
import { VIAS } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { fingerprint, onDoc, pause, until } from "../harness/wait.js";

const UNANSWERED = 5_600;

describe.each(VIAS)("file rooms: versions the drive confirms over %s", (via) => {
  const s = suite(via, "File versions");

  test("the base rises only to a version the drive confirms, and only the writer is trusted while the drive can't answer", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "verified-base", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.base === 4 && said.dirty === false, "clean at 4");
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 9 });
    await pause(400);
    expect(writerOf(writer)?.base).toBe(4);

    await s.world.setVersions(item.id, 5, 5, ben);
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    other.send({ type: "file_saved", fingerprint: "elsewhere", version: 7 });
    await pause(400);
    expect(writerOf(other)?.base).toBe(4);
    other.send({ type: "file_saved", fingerprint: "elsewhere", version: 5 });
    await writerSays(other, (said) => said.base === 5, "the base at 5");

    await s.world.setVersions(item.id, 8, 8, ben);
    const unlock = await s.world.lockDrive();
    try {
      other.send({ type: "file_saved", fingerprint: "elsewhere", version: 8 });
      await pause(UNANSWERED);
      expect(writerOf(writer)?.base).toBe(5);
      writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 8 });
      await writerSays(writer, (said) => said.base === 8, "the writer's word", UNANSWERED + 5_000);
    } finally {
      await unlock();
    }
  });

  test("a copy's word raises the base only from whoever wrote the drive's content, and after a rename only the writer's does", async () => {
    const { ada, ben, cyd } = s.world.people;
    const item = await fileItem(s, "verified-claimant", "app", 4, ada);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.base === 4 && said.dirty === false, "clean at 4");
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;

    await s.world.setVersions(item.id, 5, 5, cyd);
    other.send({ type: "file_saved", fingerprint: "elsewhere", version: 5 });
    await pause(400);
    expect(writerOf(writer)?.base).toBe(4);

    await s.world.setVersions(item.id, 5, 5, ben);
    other.send({ type: "file_saved", fingerprint: "elsewhere", version: 5 });
    await writerSays(writer, (said) => said.base === 5, "the base at 5");
    await writerSays(other, (said) => said.base === 5, "the other's base at 5");

    await s.world.setVersions(item.id, 7, 6, ben);
    other.send({ type: "file_saved", fingerprint: "elsewhere", version: 6 });
    await pause(400);
    expect(writerOf(writer)?.base).toBe(5);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 6 });
    await writerSays(writer, (said) => said.base === 6, "the writer's base at 6");
  });

  test("a kept copy is taken, and the room reloads for it, only once the drive has reached that version", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "verified-kept", "app", 4);
    const writer = await fileRoom(s, ada, item, "app");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    const generation = writerOf(writer)?.generation;
    loadApp(writer, 1, 4);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc), version: 4 });
    await writerSays(writer, (said) => said.base === 4 && said.dirty === false, "clean at 4");
    const other = await fileRoom(s, ben, item, "app");
    await other.room.synced;
    writer.doc.getMap("content").set("value", 2);
    await until(() => other.doc.getMap("content").get("value") === 2, [onDoc(other.doc)], 10_000, "the edit");
    writer.send({ type: "file_resolve", version: 9 });
    await heard(other, "file_resolving", (message) => message.version === 9);

    const kept = fingerprint(writer.doc);
    const keptMessage = { type: "file_kept", version: 9, kept_name: "a (conflicted copy).bin", fingerprint: kept };
    const reload = { type: "file_reload", version: 9, kept: true, fingerprint: kept, kept_name: "a (conflicted copy).bin" };
    other.send({ ...keptMessage, kept_name: "fake.bin" });
    writer.send(keptMessage);
    writer.send(reload);
    await pause(500);
    expect(writerOf(other)?.kept_name).toBeUndefined();
    expect(writerOf(other)?.generation).toBe(generation);

    await s.world.setVersions(item.id, 9, 9, ben);
    writer.send(keptMessage);
    await writerSays(other, (said) => said.kept_name === "a (conflicted copy).bin", "the kept name");
    writer.send(reload);
    await writerSays(other, (said) => said.generation !== generation, "the new generation");
    expect(writerOf(other)?.kept_name).toBe("a (conflicted copy).bin");
  });

  test("a copy's word stops a text room's saves only once the drive shows a real change past its save", async () => {
    const { ada, ben } = s.world.people;
    const item = await fileItem(s, "text-verified-outside", "text", 3);
    const writer = await fileRoom(s, ada, item, "text");
    await writerSays(writer, (said) => said.writer === true, "the writer");
    loadText(writer, "hello", 3);
    writer.send({ type: "file_saved", fingerprint: fingerprint(writer.doc) });
    await writerSays(writer, (said) => said.loaded === true && said.dirty === false, "clean");
    const other = await fileRoom(s, ben, item, "text");
    await textIs(other, "text", "hello");

    other.send({ type: "file_outside", version: 8 });
    await pause(400);
    expect(writerOf(writer)?.outside).not.toBe(true);

    const unlock = await s.world.lockDrive();
    try {
      other.send({ type: "file_outside", version: 8 });
      await pause(UNANSWERED);
      expect(writerOf(writer)?.outside).not.toBe(true);
    } finally {
      await unlock();
    }

    await s.world.setVersions(item.id, 8, 3);
    other.send({ type: "file_outside", version: 8 });
    await pause(400);
    expect(writerOf(writer)?.outside).not.toBe(true);

    await s.world.setVersions(item.id, 8, 8);
    other.send({ type: "file_outside", version: 8 });
    await writerSays(writer, (said) => said.outside === true, "outside");
    expect(writerOf(writer)?.drive_version).toBe(8);
  });
});
