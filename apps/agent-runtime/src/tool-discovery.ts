/** Bounded pagination must fail explicitly, never silently truncate a catalog. */
export async function collectToolPages<T extends { name: string }>(
  list: (cursor?: string) => Promise<{ tools: T[]; nextCursor?: string }>,
): Promise<T[]> {
  const items: T[] = [];
  const names = new Set<string>();
  const cursors = new Set<string>();
  let cursor: string | undefined;
  for (let page = 0; page < 100; page++) {
    const result = await list(cursor);
    for (const item of result.tools) {
      if (names.has(item.name))
        throw new Error("duplicate_tool: ambiguous capability catalog");
      names.add(item.name);
      items.push(item);
      if (items.length > 1000)
        throw new Error(
          "tool_catalog_limit: narrow the run's capability scope",
        );
    }
    if (!result.nextCursor) return items;
    if (cursors.has(result.nextCursor))
      throw new Error("invalid_tool_cursor: repeated catalog page");
    cursors.add(result.nextCursor);
    cursor = result.nextCursor;
  }
  throw new Error("tool_catalog_limit: too many catalog pages");
}
