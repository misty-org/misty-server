import { describe, expect, test } from "vitest";
import * as Y from "yjs";
import { storedDoc, textIs } from "../harness/helpers.js";
import { connect, direct, VIAS, type MistyClient } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { call } from "../harness/world.js";

describe.each(VIAS)("one-shot reads and changes beside %s rooms", (via) => {
  const s = suite(via, "Requests");

  const as = async (person = s.world.people.ada, token?: string) =>
    direct({ via, url: s.instance.url, space: s.world.space, token: token ?? (await s.world.token(person)).token, person });

  test("a room that was never opened reads as nothing", async () => {
    const misty = await as();
    try {
      expect(await misty.read(s.key("never"))).toBeNull();
      expect(await misty.read(s.key("never"), { as: "json" })).toBeNull();
    } finally {
      misty.close();
    }
  });

  test("a change makes the room with its options, saves before answering, and reaches the people in it", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("change");
    const misty = await as(ada);
    try {
      await misty.change(key, (doc) => doc.getText("title").insert(0, "Q4 "), { history: true });
      const live = await s.open(ben, key, { options: { history: true } });
      await live.room.synced;
      await textIs(live, "title", "Q4 ");
      expect(live.room.latest?.options).toMatchObject({ history: true });
      const roomId = live.room.latest?.room ?? "";
      expect((await storedDoc(roomId))?.getText("title").toString()).toBe("Q4 ");
      live.doc.getText("title").insert(3, "plan");
      live.doc.getMap("meta").set("owner", "ben");
      live.doc.getArray("steps").push(["draft"]);
      await textIs(live, "title", "Q4 plan");
      await misty.change(key, (doc) => doc.getText("title").insert(doc.getText("title").length, "!"));
      await textIs(live, "title", "Q4 plan!");
      const read = (await misty.read(key)) as Y.Doc;
      expect(read.getText("title").toString()).toBe("Q4 plan!");
      expect(read.getMap("meta").get("owner")).toBe("ben");
      const json = (await misty.read(key, { as: "json" })) as Record<string, unknown>;
      expect(json).toMatchObject({ title: "Q4 plan!", meta: { owner: "ben" }, steps: ["draft"] });
      await expect(misty.change(key, (doc) => doc.getText("title").insert(0, "x"), { history: false })).rejects.toMatchObject({
        code: "conflict",
      });
    } finally {
      misty.close();
    }
  });

  test("a change past 8 MiB is refused as too large and nothing of it is kept", async () => {
    const misty = await as();
    const key = s.key("too-large");
    try {
      await misty.change(key, (doc) => doc.getText("content").insert(0, "small"));
      await expect(misty.change(key, (doc) => doc.getText("content").insert(0, "x".repeat(9 * 1024 * 1024)))).rejects.toMatchObject({
        code: "too_large",
      });
      const json = (await misty.read(key, { as: "json" })) as Record<string, unknown>;
      expect(json.content).toBe("small");
    } finally {
      misty.close();
    }
  });

  test("a read-only integration reads a room but its change is refused", async () => {
    const { ada } = s.world.people;
    const key = s.key("integration");
    const writer = await as(ada);
    await writer.change(key, (doc) => doc.getText("content").insert(0, "for bots"));
    writer.close();
    const made = await call<{ id: string }>("/integrations", {
      owner: ada.id, space: s.world.space, name: "Webhook", access: ["rooms.read"], key_prefix: "",
    });
    const token = (await call<{ token: string }>("/tokens/integration", { owner: ada.id, space: s.world.space, id: made.id })).token;
    const bot = await as(ada, token);
    try {
      expect(await bot.read(key, { as: "json" })).toMatchObject({ content: "for bots" });
      await expect(bot.change(key, (doc) => doc.getText("content").insert(0, "x"))).rejects.toMatchObject({ code: "permission_denied" });
    } finally {
      bot.close();
    }
  });

  test("someone outside the Space can neither read nor change its rooms", async () => {
    const { eve } = s.world.people;
    const outsider = await as(eve);
    try {
      await expect(outsider.read(s.key("outside"))).rejects.toMatchObject({ code: "permission_denied" });
      await expect(outsider.change(s.key("outside"), (doc) => doc.getText("t").insert(0, "x"))).rejects.toMatchObject({
        code: "permission_denied",
      });
    } finally {
      outsider.close();
    }
  });

  test("connect({ server, token }) finds the room address in the API's health and opens rooms over the chosen transport", async () => {
    const { ada, ben } = s.world.people;
    const { token } = await s.world.token(ada);
    const api = "http://api.collab.test";
    const asked: string[] = [];
    const fetcher: typeof fetch = async (input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      if (url.startsWith(api)) {
        asked.push(new URL(url).pathname);
        if (url === `${api}/v1/me`) return Response.json({ id: ada.id, username: ada.username });
        if (url === `${api}/v1/health`) return Response.json({ status: "ok", collab_url: s.instance.url });
        return new Response("{}", { status: 404 });
      }
      return fetch(input, init);
    };
    const misty: MistyClient = await connect({ server: api, space: s.world.space, token, via, fetch: fetcher });
    try {
      expect(misty.identity).toMatchObject({ user_id: ada.id, name: ada.username });
      expect(asked).toEqual(["/v1/me", "/v1/health"]);
      const key = s.key("connect");
      const room = misty.room(key, { history: true });
      await room.synced;
      room.doc.getText("content").insert(0, "through connect");
      const watcher = await s.open(ben, key);
      await watcher.room.synced;
      await textIs(watcher, "content", "through connect");
      expect(await misty.read(key, { as: "json" })).toMatchObject({ content: "through connect" });
      room.destroy();
    } finally {
      misty.close();
    }
    const named = await connect({ server: api, space: s.world.space, token, via, collab: s.instance.url, fetch: fetcher });
    try {
      expect(asked.filter((path) => path === "/v1/health")).toHaveLength(1);
      await named.change(s.key("named"), (doc) => doc.getText("t").insert(0, "named"));
    } finally {
      named.close();
    }
  });
});
