/** Older Go catalogs encode an empty required slice as null. Preserve every
 * actual constraint while accepting that legacy representation during rollout.
 * Do not coerce malformed non-null constraints or property schemas.
 *
 * Providers take an object schema only: Anthropic refuses oneOf, anyOf and allOf at
 * the top level, which failed every run that reached browser tools (browser.upload's
 * exclusive sources). Misty's gateway enforces those rules before it runs a tool, so the
 * model-facing schema drops them and keeps everything inside the properties. */
export function normalizeToolInputSchema(schema: Record<string, unknown>): Record<string, unknown> {
  const combined = "oneOf" in schema || "anyOf" in schema || "allOf" in schema;
  if (!combined && schema.required !== null) return schema;
  const { oneOf: _oneOf, anyOf: _anyOf, allOf: _allOf, ...rest } = schema;
  return rest.required === null ? { ...rest, required: [] } : rest;
}
