import { expect, it } from "vitest";
import { collectToolPages } from "../src/tool-discovery.js";

it("discovers capabilities beyond the first hundred", async () => {
  const items = await collectToolPages(async (cursor) =>
    cursor
      ? { tools: [{ name: "habits.record" }] }
      : {
          tools: Array.from({ length: 100 }, (_, n) => ({
            name: `tools.${n}`,
          })),
          nextCursor: "next",
        },
  );
  expect(items.at(-1)?.name).toBe("habits.record");
  expect(items).toHaveLength(101);
});
it("rejects cyclic cursors and duplicate names", async () => {
  await expect(
    collectToolPages(async () => ({ tools: [], nextCursor: "loop" })),
  ).rejects.toThrow("invalid_tool_cursor");
  await expect(
    collectToolPages(async () => ({ tools: [{ name: "x" }, { name: "x" }] })),
  ).rejects.toThrow("duplicate_tool");
});
