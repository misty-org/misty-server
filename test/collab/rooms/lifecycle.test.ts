import { describe, expect, test } from "vitest";
import * as Y from "yjs";
import { closedWith, docWith, notify, saved, storedDoc, textIs } from "../harness/helpers.js";
import { startCollab } from "../harness/collab.js";
import { CLOSE_CODES, VIAS } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { pause } from "../harness/wait.js";
import { call, world } from "../harness/world.js";

describe.each(VIAS)("room lifecycle over %s", (via) => {
  const s = suite(via, "Lifecycle");

  test("a room with gc off shares nested values, saves them, and keeps them across a restart of misty-collab", async () => {
    const { ada, ben } = s.world.people;
    const own = await startCollab();
    const key = s.key("restart");
    const options = { gc: false };
    const a = await s.open(ada, key, { url: own.url, options });
    const b = await s.open(ben, key, { url: own.url, options });
    await Promise.all([a.room.synced, b.room.synced]);
    expect(a.doc.gc).toBe(false);
    a.doc.getMap("files").set("src/answer.ts", new Y.Text("export const answer = 1;\n"));
    await docWith(b.doc, () => b.doc.getMap("files").has("src/answer.ts"), "the shared file");
    await saved(b, () => {
      (b.doc.getMap("files").get("src/answer.ts") as Y.Text).insert(0, "// shared\n");
      b.doc.getArray("steps").push([{ id: "step-1", path: "src/answer.ts", summary: "Add a comment" }]);
    });
    await docWith(a.doc, () => a.doc.getArray("steps").length === 1, "the step");
    const roomId = a.room.latest?.room ?? "";
    a.close();
    b.close();
    expect(await own.stop()).toBe(0);
    const stored = await storedDoc(roomId);
    expect((stored?.getMap("files").get("src/answer.ts") as Y.Text | undefined)?.toString()).toBe("// shared\nexport const answer = 1;\n");

    const again = await startCollab();
    try {
      const restored = await s.open(ada, key, { url: again.url, options });
      await restored.room.synced;
      await docWith(
        restored.doc,
        () => (restored.doc.getMap("files").get("src/answer.ts") as Y.Text | undefined)?.toString() === "// shared\nexport const answer = 1;\n",
        "the restored file",
      );
      expect(restored.doc.getArray("steps").toJSON()).toEqual([{ id: "step-1", path: "src/answer.ts", summary: "Add a comment" }]);
      expect(restored.room.latest?.room).toBe(roomId);
    } finally {
      await again.stop();
    }
  });

  test("deleting the Space closes its rooms as gone, keeps nothing they hadn't saved, and refuses them afterwards", async () => {
    const doomed = await world("Doomed");
    const { ada, ben } = doomed.people;
    const key = s.key("deleted");
    const a = await s.open(ada, key, { space: doomed.space, token: (await doomed.token(ada)).token });
    const b = await s.open(ben, key, { space: doomed.space, token: (await doomed.token(ben)).token });
    await Promise.all([a.room.synced, b.room.synced]);
    await saved(a, () => a.doc.getText("content").insert(0, "saved"));
    a.doc.getText("content").insert(5, " unsaved");
    await textIs(b, "content", "saved unsaved");
    const roomId = a.room.latest?.room ?? "";
    await call("/spaces/delete", { owner: ada.id, space: doomed.space, name: doomed.name });
    await closedWith(a, CLOSE_CODES.gone);
    await closedWith(b, CLOSE_CODES.gone);
    expect(a.room.lost?.code).toBe("not_found");
    await pause(2_500);
    expect((await storedDoc(roomId))?.getText("content").toString()).toBe("saved");
    const after = await s.open(ada, key, { space: doomed.space, token: (await doomed.token(ada)).token });
    await closedWith(after, CLOSE_CODES.not_found);
    expect(after.opens.items).toEqual([]);
  });

  test("a purged room closes as gone and drops what it hadn't saved", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("purged");
    const a = await s.open(ada, key);
    const b = await s.open(ben, key);
    await Promise.all([a.room.synced, b.room.synced]);
    await saved(a, () => a.doc.getText("content").insert(0, "kept"));
    a.doc.getText("content").insert(4, " dropped");
    await textIs(b, "content", "kept dropped");
    const roomId = a.room.latest?.room ?? "";
    await notify({ type: "collab.purged", space_id: s.world.space, room_id: roomId });
    await closedWith(a, CLOSE_CODES.gone);
    await closedWith(b, CLOSE_CODES.gone);
    await pause(2_500);
    expect((await storedDoc(roomId))?.getText("content").toString()).toBe("kept");
    const reopened = await s.open(ben, key);
    await reopened.room.synced;
    await textIs(reopened, "content", "kept");
  });

  test("history lists versions, reads one and restores it, and only an editor may restore", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("history");
    const a = await s.open(ada, key, { options: { history: true } });
    const b = await s.open(ben, key, { options: { history: true } });
    await Promise.all([a.room.synced, b.room.synced]);
    expect(await a.room.history.list()).toEqual([]);
    await saved(a, () => a.doc.getText("content").insert(0, "first draft"));
    await saved(a, () => {
      const text = a.doc.getText("content");
      text.delete(0, text.length);
      text.insert(0, "second draft");
    });
    await textIs(b, "content", "second draft");
    const versions = await b.room.history.list();
    expect(versions).toHaveLength(1);
    const [version] = versions;
    expect(version?.id).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(version?.size).toBeGreaterThan(0);
    expect(Number.isNaN(Date.parse(version?.at ?? ""))).toBe(false);
    expect(version?.by).toContain(ada.id);
    const read = await b.room.history.read(version?.id ?? "");
    expect(read.getText("content").toString()).toBe("first draft");
    await expect(b.room.history.read("no-such-version")).rejects.toMatchObject({ code: "not_found" });

    const made = await call<{ id: string }>("/integrations", {
      owner: ada.id, space: s.world.space, name: "History reader", access: ["rooms.read"], key_prefix: "",
    });
    const token = (await call<{ token: string }>("/tokens/integration", { owner: ada.id, space: s.world.space, id: made.id })).token;
    const reader = await s.open(ada, key, { token, person: undefined });
    await reader.room.synced;
    expect(await reader.room.history.list()).toHaveLength(1);
    await expect(reader.room.history.restore(version?.id ?? "")).rejects.toMatchObject({ code: "permission_denied" });

    await b.room.history.restore(version?.id ?? "");
    await textIs(a, "content", "first draft");
    await textIs(b, "content", "first draft");
    await textIs(reader, "content", "first draft");
  });

  test("a room opened without history has none to list", async () => {
    const { ada } = s.world.people;
    const a = await s.open(ada, s.key("no-history"));
    await a.room.synced;
    await expect(a.room.history.list()).rejects.toBeTruthy();
  });
});
