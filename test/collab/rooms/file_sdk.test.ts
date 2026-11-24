import { afterEach, describe, expect, test } from "vitest";
import { direct, VIAS, type FileRoom, type MistyClient } from "../harness/sdk.js";
import { driveStore, type DriveStore } from "../harness/store.js";
import { suite } from "../harness/suite.js";
import { onDoc, pause, until } from "../harness/wait.js";
import { driveUntil, sha256, type DriveItem } from "../harness/world.js";
import type { Person } from "../harness/global_setup.js";

const textOf = (room: FileRoom) => room.text.toString();

const lostWith = (room: FileRoom, timeoutMs = 45_000) =>
  new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("the file room was not reloaded")), timeoutMs);
    room.onLost((error) => {
      clearTimeout(timer);
      resolve(error.code);
    });
  });

describe.each(VIAS)("file rooms through the SDK over %s", (via) => {
  const s = suite(via, "File SDK");
  const contents = new Map<string, Uint8Array>();
  const opened: { misty: MistyClient; store: DriveStore }[] = [];

  afterEach(() => {
    for (const { misty, store } of opened.splice(0)) {
      misty.close();
      store.close();
    }
  });

  const connectAs = async (person: Person) => {
    const store = driveStore(s.world, person, contents);
    const misty = await direct({ via, url: s.instance.url, space: s.world.space, token: (await s.world.token(person)).token, person, files: store });
    opened.push({ misty, store });
    return { misty, store };
  };

  const openFile = async (person: Person, item: DriveItem) => {
    const { misty, store } = await connectAs(person);
    const room = misty.room.forFile(item.id);
    await room.ready;
    return { room, misty, store };
  };

  const textFile = async (label: string, text: string) => {
    const item = await s.world.file(`${s.key(label)}.md`, text);
    contents.set(sha256(text), new TextEncoder().encode(text));
    return item;
  };

  test("the server saves a text file room's text into the drive", async () => {
    const { ada, ben } = s.world.people;
    const item = await textFile("server-saved", "hello");
    const a = await openFile(ada, item);
    expect(a.room.format).toBe("text");
    expect(textOf(a.room)).toBe("hello");
    const b = await openFile(ben, item);
    expect(textOf(b.room)).toBe("hello");
    a.room.text.insert(5, " world");
    await until(() => textOf(b.room) === "hello world", [onDoc(b.room.doc)], 10_000, "the edit");
    await driveUntil(async () => (await s.world.item(item.id)).sha256 === sha256("hello world"), 20_000, "the drive to hold the text");
    const saved = await s.world.item(item.id);
    expect(saved).toMatchObject({ version: 2, content_version: 2, size: 11, modified_by: ada.id });
    b.room.text.insert(0, "> ");
    await driveUntil(async () => (await s.world.item(item.id)).sha256 === sha256("> hello world"), 20_000, "the second save");
    expect((await s.world.item(item.id)).version).toBe(3);
  });

  test("a text file changed outside reloads both people's rooms, and reopening shows the drive's text once", async () => {
    const { ada, ben, cyd } = s.world.people;
    const item = await textFile("reload-two", "hello");
    const a = await openFile(ada, item);
    const b = await openFile(ben, item);
    expect([textOf(a.room), textOf(b.room)]).toEqual(["hello", "hello"]);
    const lostA = lostWith(a.room);
    const lostB = lostWith(b.room);
    const drive = "hello from the drive";
    contents.set(sha256(drive), new TextEncoder().encode(drive));
    await s.world.write(item.id, drive, cyd);
    expect(await lostA).toBe("reloaded");
    expect(await lostB).toBe("reloaded");
    expect(a.misty.closes.items.every((close) => close.code === 4010 || close.code === 1000)).toBe(true);

    const again = await openFile(ada, item);
    const other = await openFile(ben, item);
    expect(textOf(again.room)).toBe(drive);
    expect(textOf(other.room)).toBe(drive);
    await pause(500);
    expect(textOf(again.room)).toBe(drive);
    expect(textOf(other.room)).toBe(drive);
    expect(await again.misty.read(item.id, { as: "json" })).toMatchObject({ text: drive });
  }, 90_000);

  test("a text file changed outside while someone typed keeps their text beside it, then both reopen to the drive's text once", async () => {
    const { ada, ben, cyd } = s.world.people;
    const item = await textFile("resolve-two", "hello");
    const a = await openFile(ada, item);
    const b = await openFile(ben, item);
    const lostA = lostWith(a.room);
    const lostB = lostWith(b.room);
    const drive = "theirs";
    contents.set(sha256(drive), new TextEncoder().encode(drive));
    await s.world.write(item.id, drive, cyd);
    a.room.text.insert(5, " mine");
    expect(await lostA).toBe("reloaded");
    expect(await lostB).toBe("reloaded");
    const copies = await s.world.copies(item);
    expect(copies.map((copy) => copy.sha256)).toContain(sha256("hello mine"));

    const again = await openFile(ada, item);
    const other = await openFile(ben, item);
    expect(textOf(again.room)).toBe(drive);
    expect(textOf(other.room)).toBe(drive);
    await pause(500);
    expect(textOf(again.room)).toBe(drive);
    expect(textOf(other.room)).toBe(drive);
    expect((await s.world.item(item.id)).sha256).toBe(sha256(drive));
  }, 90_000);
});
