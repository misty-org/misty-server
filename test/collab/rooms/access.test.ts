import { describe, expect, test } from "vitest";
import * as Y from "yjs";
import { closedWith, textIs } from "../harness/helpers.js";
import { CLOSE_CODES, VIAS, type Client } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { onDoc, pause, until } from "../harness/wait.js";
import { call, world } from "../harness/world.js";

describe.each(VIAS)("admission and access over %s", (via) => {
  const s = suite(via, "Access");

  test("connections that aren't admitted receive nothing from the room", async () => {
    const { ada, eve } = s.world.people;
    const key = s.key("unadmitted");
    const writer = await s.open(ada, key);
    writer.room.awareness.setLocalStateField("user", { name: "Ada" });
    await writer.room.synced;
    writer.doc.getText("content").insert(0, "private");
    let step = 0;
    const typing = setInterval(() => {
      step += 1;
      writer.doc.getText("content").insert(0, "x");
      writer.room.awareness.setLocalStateField("step", step);
    }, 2);
    try {
      const outsider = (await s.world.token(eve)).token;
      const other = await world("Elsewhere");
      const elsewhere = (await other.token(eve)).token;
      const refused: Client[] = [];
      for (let index = 0; index < 12; index += 1) {
        const token = index % 3 === 0 ? "not-a-token" : index % 3 === 1 ? outsider : elsewhere;
        refused.push(await s.open(eve, key, { token }));
      }
      await Promise.all(refused.map((each) => until(() => each.closes.items.length > 0, [each.closes.source], 15_000, "a refusal")));
      for (const [index, each] of refused.entries()) {
        expect(each.closes.items[0]?.code).toBe(index % 3 === 0 ? CLOSE_CODES.unauthorized : CLOSE_CODES.forbidden);
        expect(each.frames.items).toEqual([]);
        expect(each.opens.items).toEqual([]);
        expect(each.doc.getText("content").length).toBe(0);
        expect(each.room.awareness.getStates().size).toBeLessThanOrEqual(1);
        expect(each.room.lost?.code).toBe("permission_denied");
      }
    } finally {
      clearInterval(typing);
    }
  });

  test("a room opened again with other options is refused with options_conflict, and options left out take the room's", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("options");
    const first = await s.open(ada, key, { options: { history: true, gc: false } });
    await first.room.synced;
    expect(first.room.latest?.options).toMatchObject({ history: true, gc: false });
    const differs = await s.open(ben, key, { options: { history: false } });
    await closedWith(differs, CLOSE_CODES.options_conflict);
    expect(differs.room.lost?.code).toBe("conflict");
    const gc = await s.open(ben, key, { options: { gc: true } });
    await closedWith(gc, CLOSE_CODES.options_conflict);
    const plain = await s.open(ben, key);
    await plain.room.synced;
    expect(plain.room.latest?.options).toMatchObject({ history: true, gc: false });
  });

  test("the 41st connection is refused while all 40 seats are taken", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("seats");
    const token = (await s.world.token(ada)).token;
    const seated: Client[] = [];
    for (let index = 0; index < 40; index += 1) seated.push(await s.open(ada, key, { token }));
    await Promise.all(seated.map((each) => each.room.synced));
    const late = await s.open(ben, key);
    await closedWith(late, CLOSE_CODES.full);
    await until(() => late.problems.some((problem) => problem.code === "stalled"), [late.problems.source], 5_000, "the full room's notice");
    expect(late.opens.items).toEqual([]);
    seated[0]?.doc.getText("content").insert(0, "forty");
    await textIs(seated[39] as Client, "content", "forty");
  });

  test("an update from client id 0 is refused with bad_frame and never reaches anyone", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("client-zero");
    const watcher = await s.open(ben, key);
    await watcher.room.synced;
    const zero = new Y.Doc();
    zero.clientID = 0;
    const bad = await s.open(ada, key, { doc: zero });
    await bad.room.synced;
    zero.getText("content").insert(0, "from zero");
    await closedWith(bad, CLOSE_CODES.bad_frame);
    await pause(300);
    expect(watcher.doc.getText("content").toString()).toBe("");
  });

  test("signing a session out closes its connections with signed_out and leaves the person's other sessions open", async () => {
    const { ada } = s.world.people;
    const key = s.key("signed-out");
    const ended = await s.world.token(ada);
    const kept = await s.world.token(ada);
    const gone = await s.open(ada, key, { token: ended.token });
    const stays = await s.open(ada, key, { token: kept.token });
    await Promise.all([gone.room.synced, stays.room.synced]);
    await call("/signals/session", { session: ended.session });
    await closedWith(gone, CLOSE_CODES.signed_out);
    expect(gone.room.lost?.code).toBe("permission_denied");
    await pause(300);
    expect(stays.closes.items).toEqual([]);
    stays.doc.getText("content").insert(0, "still here");
    await until(() => stays.room.connected, [stays.closes.source], 2_000, "the other session to stay");
  });

  test("an integration whose name changes signs in again with a fresh token and comes back; one that is removed is signed out", async () => {
    const { ada } = s.world.people;
    const key = s.key("reauth");
    const made = await call<{ id: string }>("/integrations", {
      owner: ada.id, space: s.world.space, name: "Deploy bot", access: ["rooms.read", "rooms.write"], key_prefix: "",
    });
    const asked: boolean[] = [];
    const token = async (options?: { fresh?: boolean }) => {
      asked.push(options?.fresh === true);
      return (await call<{ token: string }>("/tokens/integration", { owner: ada.id, space: s.world.space, id: made.id })).token;
    };
    const bot = await s.open(ada, key, { token, person: undefined });
    await bot.room.synced;
    expect(bot.room.latest?.identity).toMatchObject({ kind: "integration", id: made.id, name: "Deploy bot" });
    await call("/integrations/rename", { owner: ada.id, space: s.world.space, id: made.id, name: "Release bot" });
    const before = asked.length;
    await closedWith(bot, CLOSE_CODES.reauth);
    await until(() => bot.opens.items.length === 2, [bot.opens.source], 15_000, "the integration to come back");
    expect(asked.slice(0, before)).not.toContain(true);
    expect(asked.slice(before)).toContain(true);
    expect(bot.room.latest?.identity.name).toBe("Release bot");
    await call("/signals/integration", { owner: ada.id, space: s.world.space, id: made.id });
    await closedWith(bot, CLOSE_CODES.signed_out);
    expect(bot.room.lost?.code).toBe("permission_denied");
  });

  test("a read-only integration sees the room but its edits are dropped with read_only", async () => {
    const { ada } = s.world.people;
    const key = s.key("read-only");
    const editor = await s.open(ada, key);
    await editor.room.synced;
    editor.doc.getText("content").insert(0, "visible");
    const made = await call<{ id: string }>("/integrations", {
      owner: ada.id, space: s.world.space, name: "Reader", access: ["rooms.read"], key_prefix: "",
    });
    const token = (await call<{ token: string }>("/tokens/integration", { owner: ada.id, space: s.world.space, id: made.id })).token;
    const reader = await s.open(ada, key, { token, person: undefined });
    await reader.room.synced;
    await textIs(reader, "content", "visible");
    expect(reader.room.latest?.role).toBe("view");
    reader.doc.getText("content").insert(0, "nope ");
    await until(() => reader.problems.some((problem) => problem.code === "read_only"), [reader.problems.source], 5_000, "read_only");
    await pause(300);
    expect(editor.doc.getText("content").toString()).toBe("visible");
  });

  test("an integration is refused outside its key prefix", async () => {
    const { ada } = s.world.people;
    const made = await call<{ id: string }>("/integrations", {
      owner: ada.id, space: s.world.space, name: "Board bot", access: ["rooms.write"], key_prefix: "app:board:",
    });
    const token = (await call<{ token: string }>("/tokens/integration", { owner: ada.id, space: s.world.space, id: made.id })).token;
    const inside = await s.open(ada, `app:board:${s.key("inside")}`, { token, person: undefined });
    await inside.room.synced;
    const outside = await s.open(ada, s.key("outside"), { token, person: undefined });
    await closedWith(outside, CLOSE_CODES.forbidden);
  });

  test("removing a member closes their connections with access_changed and leaves everyone else", async () => {
    const own = await world("Removal");
    const { ada, ben } = own.people;
    const key = s.key("removed");
    const stays = await s.open(ada, key, { space: own.space, token: (await own.token(ada)).token });
    const leaves = await s.open(ben, key, { space: own.space, token: (await own.token(ben)).token });
    await Promise.all([stays.room.synced, leaves.room.synced]);
    await call("/members/remove", { owner: ada.id, space: own.space, member: ben.id });
    await closedWith(leaves, CLOSE_CODES.access_changed);
    expect(leaves.room.lost?.code).toBe("permission_denied");
    await pause(300);
    expect(stays.closes.items).toEqual([]);
    const again = await s.open(ben, key, { space: own.space, token: (await own.token(ben)).token });
    await closedWith(again, CLOSE_CODES.forbidden);
  });
});

describe("admission over a raw WebSocket", () => {
  const s = suite("websocket", "Raw");

  const raw = async (key: string, frames: (socket: WebSocket) => void) => {
    const { token } = await s.world.token(s.world.people.ada);
    const socket = new WebSocket(`${s.instance.url.replace("http", "ws")}/v1/collab`);
    socket.binaryType = "arraybuffer";
    const received: unknown[] = [];
    socket.addEventListener("message", (event) => received.push(event.data));
    const closed = new Promise<CloseEvent>((resolve) => socket.addEventListener("close", resolve));
    socket.addEventListener("open", () => {
      socket.send(JSON.stringify({ type: "open", v: 1, space: s.world.space, key, options: {}, token, client: "collab-runtime-test" }));
      frames(socket);
    });
    return { closed, received };
  };

  test("a socket whose first message isn't open is closed with bad_open", async () => {
    const socket = new WebSocket(`${s.instance.url.replace("http", "ws")}/v1/collab`);
    const closed = new Promise<CloseEvent>((resolve) => socket.addEventListener("close", resolve));
    socket.addEventListener("open", () => socket.send(new Uint8Array([0, 0, 1, 0])));
    expect((await closed).code).toBe(CLOSE_CODES.bad_open);
  });

  test("more than 64 frames sent before the room opens closes the socket with too_large", async () => {
    const { closed, received } = await raw(s.key("early-frames"), (socket) => {
      for (let index = 0; index < 80; index += 1) socket.send(new Uint8Array([3]));
    });
    expect((await closed).code).toBe(CLOSE_CODES.too_large);
    expect(received.filter((data) => typeof data !== "string")).toEqual([]);
  });

  test("more than 1 MiB sent before the room opens closes the socket with too_large", async () => {
    const { closed } = await raw(s.key("early-bytes"), (socket) => {
      socket.send(new Uint8Array(1024 * 1024 + 16).fill(3));
    });
    expect((await closed).code).toBe(CLOSE_CODES.too_large);
  });
});
