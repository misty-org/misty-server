/** Older Go catalogs encode an empty required slice as null. Preserve every
 * actual constraint while accepting that legacy representation during rollout.
 * Do not coerce malformed non-null constraints or property schemas. */
export function normalizeToolInputSchema(schema: Record<string, unknown>): Record<string, unknown> {
  return schema.required === null ? { ...schema, required: [] } : schema;
}
