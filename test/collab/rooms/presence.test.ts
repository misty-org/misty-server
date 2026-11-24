import { describe, expect, test } from "vitest";
import * as Y from "yjs";
import { presence, sees, states, textIs } from "../harness/helpers.js";
import { VIAS } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { onDoc, pause, until } from "../harness/wait.js";

describe.each(VIAS)("rooms and presence over %s", (via) => {
  const s = suite(via, "Presence");

  test("two people edit one room and each carries the identity the server gave them", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("shared");
    const a = await s.open(ada, key);
    const b = await s.open(ben, key);
    await Promise.all([a.room.synced, b.room.synced]);
    a.room.awareness.setLocalStateField("user", { name: "Ada" });
    a.doc.getText("content").insert(0, "hello");
    await textIs(b, "content", "hello");
    b.doc.getText("content").insert(5, " there");
    await textIs(a, "content", "hello there");
    await presence(b, () => sees(b, ada.id, "Ada"), "Ada's presence");
    expect(a.room.latest?.identity).toMatchObject({ kind: "person", id: ada.id, name: ada.username });
    expect(a.room.latest?.role).toBe("edit");
    expect(a.room.latest?.seats).toBe(40);
    expect(a.room.latest?.max_bytes).toBe(8 * 1024 * 1024);
  });

  test("a person who comes back with a fresh token shows up once, with no ghost of the old connection", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("reconnect");
    const a = await s.open(ada, key);
    a.room.awareness.setLocalStateField("user", { name: "Ada" });
    await a.room.synced;
    a.doc.getText("content").insert(0, "survives reconnect");
    const b = await s.open(ben, key);
    b.room.awareness.setLocalStateField("user", { name: "Ben" });
    await b.room.synced;
    await textIs(b, "content", "survives reconnect");
    await presence(b, () => sees(b, ada.id, "Ada"), "Ada's presence");
    a.close();
    await presence(b, () => !sees(b, ada.id), "Ada to leave");
    const again = await s.open(ada, key);
    again.room.awareness.setLocalStateField("user", { name: "Ada" });
    await again.room.synced;
    await textIs(again, "content", "survives reconnect");
    await presence(b, () => sees(b, ada.id, "Ada"), "Ada to come back");
    expect(states(b).filter((state) => state.misty?.id === ada.id)).toHaveLength(1);
  });

  test("a second connection of the same person stays live when the first one closes", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("same-person");
    const first = await s.open(ada, key);
    const second = await s.open(ada, key);
    const other = await s.open(ben, key);
    await Promise.all([first.room.synced, second.room.synced, other.room.synced]);
    first.close();
    await pause(200);
    other.doc.getText("content").insert(0, "still live");
    await textIs(second, "content", "still live");
  });

  test("presence sent again from a new connection leaves with that connection, and someone else can't take it over", async () => {
    const { ada, ben, cyd } = s.world.people;
    const key = s.key("presence-owner");
    const watcher = await s.open(ben, key);
    await watcher.room.synced;
    const firstDoc = new Y.Doc();
    firstDoc.clientID = 4242;
    const first = await s.open(ada, key, { doc: firstDoc });
    first.room.awareness.setLocalStateField("user", { name: "Ada" });
    await first.room.synced;
    await presence(watcher, () => sees(watcher, ada.id, "Ada"), "Ada");

    const secondDoc = new Y.Doc();
    secondDoc.clientID = 4242;
    const second = await s.open(ada, key, { doc: secondDoc });
    for (let step = 0; step < 4; step += 1) second.room.awareness.setLocalStateField("step", step);
    second.room.awareness.setLocalStateField("user", { name: "Ada again" });
    await second.room.synced;
    await presence(watcher, () => sees(watcher, ada.id, "Ada again"), "Ada's second connection");

    first.room.disconnect();
    await pause(300);
    expect(sees(watcher, ada.id, "Ada again")).toBe(true);

    const intruderDoc = new Y.Doc();
    intruderDoc.clientID = 4242;
    const intruder = await s.open(cyd, key, { doc: intruderDoc });
    for (let step = 0; step < 10; step += 1) intruder.room.awareness.setLocalStateField("step", step);
    intruder.room.awareness.setLocalStateField("user", { name: "Eve" });
    await intruder.room.synced;
    await pause(300);
    expect(states(watcher).some((state) => state.user?.name === "Eve" || state.misty?.id === cyd.id)).toBe(false);
    expect(sees(watcher, ada.id, "Ada again")).toBe(true);

    second.room.disconnect();
    await presence(watcher, () => !sees(watcher, ada.id), "Ada's presence to leave with her connection");
  });

  test("a client that writes someone else's identity into its presence is corrected", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("identity-guard");
    const watcher = await s.open(ben, key);
    await watcher.room.synced;
    const liar = await s.open(ada, key);
    liar.room.awareness.setLocalStateField("misty", { kind: "person", id: ben.id, name: ben.username });
    await liar.room.synced;
    await presence(watcher, () => sees(watcher, ada.id), "the corrected identity");
    const claimed = states(watcher).filter((state) => state.misty?.id === ben.id);
    expect(claimed).toHaveLength(0);
  });

  test("an idle connection keeps its presence for as long as it is connected", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("idle-presence");
    const watcher = await s.open(ben, key);
    await watcher.room.synced;
    const idle = await s.open(ada, key);
    idle.room.awareness.setLocalStateField("user", { name: "Ada" });
    await idle.room.synced;
    await presence(watcher, () => sees(watcher, ada.id, "Ada"), "Ada");
    await pause(35_000);
    expect(sees(watcher, ada.id, "Ada")).toBe(true);
    const late = await s.open(ben, key);
    await late.room.synced;
    await presence(late, () => sees(late, ada.id, "Ada"), "Ada for someone who joins later");
  }, 60_000);

  test("an update larger than the old 512 KB ceiling is accepted", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("large");
    const a = await s.open(ada, key);
    const b = await s.open(ben, key);
    await Promise.all([a.room.synced, b.room.synced]);
    const large = "x".repeat(600 * 1024);
    a.doc.getText("content").insert(0, large);
    await until(() => b.doc.getText("content").length === large.length, [onDoc(b.doc)], 20_000, "the large update");
    expect(a.closes.items).toEqual([]);
  });
});
