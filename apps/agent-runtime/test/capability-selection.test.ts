import { describe, expect, it } from "vitest";
import {
  capabilityPage,
  capabilityToolKey,
} from "../src/capability-selection.js";

describe("registry-driven tool selection", () => {
  it("keeps capabilities beyond the initial working set discoverable", () => {
    const catalog = Array.from({ length: 125 }, (_, index) => ({
      name: `example.operation${index}`,
      description: `Action ${index}`,
      inputSchema: { type: "object" },
    }));
    catalog.push({
      name: "habits.record",
      description: "Record today's completed habit",
      inputSchema: { type: "object" },
    });
    expect(capabilityPage(catalog, "record habit").items[0]?.name).toBe(
      "habits.record",
    );
    const seen = new Set<string>();
    let cursor: number | null = 0;
    while (cursor !== null) {
      const page = capabilityPage(catalog, "", cursor);
      page.items.forEach((item) => seen.add(item.name));
      cursor = page.nextCursor;
    }
    expect(seen.size).toBe(catalog.length);
  });
  it("uses collision-free model keys and validates paging", () => {
    expect(capabilityToolKey(1)).not.toBe(capabilityToolKey(2));
    expect(() => capabilityPage([], "", -1)).toThrow();
  });
});
