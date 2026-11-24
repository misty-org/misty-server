import { describe, expect, test } from "vitest";
import { VIAS } from "../harness/sdk.js";
import { suite } from "../harness/suite.js";
import { onDoc, pause, until } from "../harness/wait.js";

const MIB = 1024 * 1024;

describe.each(VIAS)("document size over %s", (via) => {
  const s = suite(via, "Sizes");

  test("a room growing past the warning size and then past 8 MiB says too_large_warning once, then too_large once", async () => {
    const { ada, ben } = s.world.people;
    const key = s.key("growing");
    const a = await s.open(ada, key);
    const b = await s.open(ben, key);
    await Promise.all([a.room.synced, b.room.synced]);
    const count = (code: string) => a.problems.items.filter((problem) => problem.code === code).length;

    a.doc.getText("content").insert(0, "w".repeat(7 * MIB));
    await until(() => count("too_large_warning") === 1, [a.problems.source], 30_000, "the warning");
    await until(() => b.doc.getText("content").length === 7 * MIB, [onDoc(b.doc)], 30_000, "the large text");
    a.doc.getText("content").insert(0, "w".repeat(1024));
    await until(
      () => a.messages.some((message) => message.type === "document_status" && message.status === "warning"),
      [a.messages.source],
      30_000,
      "the warning status",
    );

    a.doc.getText("content").insert(0, "b".repeat(2 * MIB));
    await until(() => count("too_large") === 1, [a.problems.source], 30_000, "too_large");
    a.doc.getText("other").insert(0, "b".repeat(2 * MIB));
    await until(
      () => a.messages.items.filter((message) => message.type === "document_status" && message.status === "blocked").length >= 2,
      [a.messages.source],
      30_000,
      "the second refusal",
    );
    await pause(3_000);
    expect(count("too_large_warning")).toBe(1);
    expect(count("too_large")).toBe(1);
    expect(b.doc.getText("content").length).toBe(7 * MIB + 1024);
    expect(b.doc.getText("content").toString().includes("b")).toBe(false);
    expect(b.doc.getText("other").length).toBe(0);
    expect(a.closes.items).toEqual([]);
  }, 120_000);
});
