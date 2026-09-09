import type { MCPRemoteTool } from "./types.js";

/** Select a bounded working set; the entire admitted catalog remains searchable. */
export function capabilityPage(
  catalog: MCPRemoteTool[],
  query: string,
  cursor = 0,
  limit = 31,
) {
  if (
    !Number.isSafeInteger(cursor) ||
    cursor < 0 ||
    !Number.isSafeInteger(limit) ||
    limit < 1 ||
    limit > 50
  )
    throw new Error("Invalid capability discovery page");
  const terms = query
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((term) => term.length > 2);
  const ranked = catalog
    .map((tool, index) => {
      const name = tool.name.toLowerCase();
      const description = tool.description.toLowerCase();
      const score = terms.reduce(
        (total, term) =>
          total +
          (name.includes(term) ? 5 : description.includes(term) ? 1 : 0),
        0,
      );
      return { tool, index, score };
    })
    .sort(
      (a, b) => b.score - a.score || a.tool.name.localeCompare(b.tool.name),
    );
  const items = ranked.slice(cursor, cursor + limit).map((item) => item.tool);
  return {
    items,
    total: ranked.length,
    nextCursor:
      cursor + items.length < ranked.length ? cursor + items.length : null,
  };
}

export function capabilityToolKey(index: number): string {
  return `capability_${index}`;
}
