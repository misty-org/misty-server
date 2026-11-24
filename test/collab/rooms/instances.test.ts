import { afterAll, beforeAll, describe, expect, test } from "vitest";
import * as Y from "yjs";
import { closedWith, storedDoc, textIs } from "../harness/helpers.js";
import { startCollab, startEdge, stopAll, type Edge, type Instance } from "../harness/collab.js";
import { client, CLOSE_CODES, direct, VIAS, type Client, type Via } from "../harness/sdk.js";
import { onDoc, until } from "../harness/wait.js";
import { closeDatabase, database, unique, world, type World } from "../harness/world.js";
import type { Person } from "../harness/global_setup.js";

const owner = async (roomId: string) =>
  String((await database().query(`SELECT owner_url FROM collab_rooms WHERE id=$1`, [roomId])).rows[0]?.owner_url ?? "");

describe.each(VIAS)("two instances over %s", (via: Via) => {
  let edge: Edge;
  let first: Instance;
  let second: Instance;
  let drive: World;
  const opened = new Set<Client>();

  beforeAll(async () => {
    edge = await startEdge();
    first = await startCollab({ public: edge.url });
    second = await startCollab({ public: edge.url });
    drive = await world("Instances");
  });

  afterAll(async () => {
    for (const each of opened) each.close();
    await stopAll();
    await edge.close();
    await closeDatabase();
  });

  const open = async (person: Person, key: string, url: string) => {
    const made = client({ via, url, space: drive.space, key, person, token: (await drive.token(person)).token });
    opened.add(made);
    return made;
  };

  test("a connection that reaches an instance that doesn't own the room is sent to the owner, and so is a one-shot request", async () => {
    const { ada, ben, cyd } = drive.people;
    const key = unique("moved");
    const a = await open(ada, key, first.url);
    await a.room.synced;
    a.doc.getText("content").insert(0, "owned by the first");
    const roomId = a.room.latest?.room ?? "";
    expect(await owner(roomId)).toBe(first.url);

    const b = await open(ben, key, second.url);
    await b.room.synced;
    expect(b.closes.items.map((close) => close.code)).toEqual([CLOSE_CODES.moved]);
    expect(b.opens.items).toEqual([first.url]);
    await textIs(b, "content", "owned by the first");

    const misty = await direct({ via, url: second.url, space: drive.space, token: (await drive.token(cyd)).token, person: cyd });
    try {
      expect(await misty.read(key, { as: "json" })).toMatchObject({ content: "owned by the first" });
      await misty.change(key, (doc) => doc.getText("content").insert(0, "> "));
      await textIs(a, "content", "> owned by the first");
      await textIs(b, "content", "> owned by the first");
    } finally {
      misty.close();
    }
  });

  test("an instance that stops hands its rooms back: everyone moves to the other one and nothing saved or typed is lost", async () => {
    const { ada, ben } = drive.people;
    const leaving = await startCollab({ public: edge.url });
    edge.to(leaving);
    const key = unique("hand-back");
    const a = await open(ada, key, edge.url);
    const b = await open(ben, key, edge.url);
    await Promise.all([a.room.synced, b.room.synced]);
    const roomId = a.room.latest?.room ?? "";
    expect(await owner(roomId)).toBe(leaving.url);
    a.doc.getText("content").insert(0, "typed before the stop");
    await textIs(b, "content", "typed before the stop");

    edge.to(second);
    const stopped = leaving.stop("SIGTERM");
    await closedWith(a, CLOSE_CODES.moved);
    await closedWith(b, CLOSE_CODES.moved);
    b.doc.getText("content").insert(0, "while moving: ");
    expect(await stopped).toBe(0);
    expect(leaving.log()).toContain("handing rooms back");
    expect((await storedDoc(roomId))?.getText("content").toString()).toContain("typed before the stop");

    await until(() => a.room.connected && b.room.connected, [a.opens.source, b.opens.source, onDoc(a.doc), onDoc(b.doc)], 20_000, "both to land on the other instance");
    await textIs(a, "content", "while moving: typed before the stop");
    await textIs(b, "content", "while moving: typed before the stop");
    expect(await owner(roomId)).toBe(second.url);
    expect(a.opens.items.at(-1)).toBe(edge.url);
    a.doc.getText("content").insert(a.doc.getText("content").length, " and after");
    await textIs(b, "content", "while moving: typed before the stop and after");
    const fresh = new Y.Doc();
    const check = client({ via, url: second.url, space: drive.space, key, person: ada, token: (await drive.token(ada)).token, doc: fresh });
    opened.add(check);
    await check.room.synced;
    await textIs(check, "content", "while moving: typed before the stop and after");
  }, 60_000);
});
