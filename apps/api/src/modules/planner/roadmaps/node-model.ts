import { z } from "zod";
import { SpaceError, trimSpace } from "../../spaces/model.js";

const text = z.string().nullable().optional().transform(value => trimSpace(value ?? ""));
const id = z.string().nullable().optional().transform(value => value ?? "");
const position = z.number().finite().nullable().optional().transform(value => value ?? 0).refine(value => Math.abs(value) <= 10000000);
const field = z.looseObject({ id: z.string(), label: z.string(), type: z.enum(["short_text", "long_text", "number", "date", "url", "select", "checkbox"]),
  options: z.array(z.string()).max(50).nullable().optional(), archived: z.boolean().nullable().optional() });
export function definitionFields(raw: unknown) {
  const parsed = z.array(field).max(20).safeParse(raw);
  if (!parsed.success || Buffer.byteLength(JSON.stringify(raw)) > 32768) throw new SpaceError("invalid_request");
  const seen = new Set<string>();
  for (const item of parsed.data) {
    const key = trimSpace(item.id), label = trimSpace(item.label), options = item.options ?? [], optionIds = new Set<string>();
    if (!/^[a-z][a-z0-9_-]{0,63}$/.test(key) || seen.has(key) || !label || [...label].length > 80 || item.type !== "select" && options.length) throw new SpaceError("invalid_request");
    seen.add(key);
    for (const rawOption of options) {
      const option = trimSpace(rawOption);
      if (!option || [...option].length > 80 || optionIds.has(option)) throw new SpaceError("invalid_request");
      optionIds.add(option);
    }
  }
  // Go validates trimmed identifiers/options but stores their original spelling.
  return parsed.data;
}
export function definitionInput(raw: unknown, update = false) {
  const parsed = z.object({ name: text, description: text, icon: text, color: text, agenda_visible: z.boolean().nullable().optional().transform(value => value ?? false),
    field_schema: z.unknown().optional().transform(value => value === undefined ? [] : value), expected_version: z.number().int().safe().positive().optional() }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request"); const value = parsed.data;
  if (!value.name || [...value.name].length > 120 || [...value.description].length > 2000 || !value.icon || [...value.icon].length > 80
    || !["slate", "blue", "cyan", "emerald", "amber", "orange", "rose", "violet"].includes(value.color) || update && !value.expected_version) throw new SpaceError("invalid_request");
  return { ...value, fields: definitionFields(value.field_schema) };
}
export function validateDefinitionUpdate(previous: unknown, next: ReturnType<typeof definitionFields>) {
  const byId = new Map(next.map(item => [item.id, item]));
  for (const item of definitionFields(previous)) if (byId.get(item.id)?.type !== item.type) throw new SpaceError("invalid_request");
}
export function nodeInput(raw: unknown) {
  const parsed = z.object({ node_kind: id, definition_id: id, milestone_id: id, title: text, description: text,
    target_date: z.iso.datetime({ offset: true }).nullable().optional(), position_x: position, position_y: position,
    field_values: z.record(z.string(), z.unknown()).default({}), expected_version: z.number().int().safe().positive() }).safeParse(raw);
  if (!parsed.success) throw new SpaceError("invalid_request"); const value = parsed.data, kind = trimSpace(value.node_kind);
  if (!["risk", "decision", "metric", "note", "custom"].includes(kind) || !value.title || [...value.title].length > 240 || [...value.description].length > 20000
    || Buffer.byteLength(JSON.stringify(value.field_values)) > 65536 || (kind === "custom" ? !value.definition_id : !!value.definition_id)) throw new SpaceError("invalid_request");
  return { ...value, raw_node_kind: value.node_kind, node_kind: kind, target_date: value.target_date?.slice(0, 10) ?? null };
}
export function validateFieldValues(values: Record<string, unknown>, rawSchema: unknown) {
  const fields = new Map(definitionFields(rawSchema).map(item => [item.id, item]));
  for (const [key, value] of Object.entries(values)) {
    const item = fields.get(key);
    if (!item) throw new SpaceError("invalid_request");
    const valid = item.type === "number" ? typeof value === "number" && Number.isFinite(value) : item.type === "checkbox" ? typeof value === "boolean"
      : typeof value === "string" && [...value].length <= 20000 && (item.type !== "select" || value === "" || (item.options ?? []).includes(value));
    if (!valid) throw new SpaceError("invalid_request");
  }
}
export const definitionColumns = "id,space_id,name,description,icon,color,agenda_visible,field_schema,version,created_by_user_id,archived_at,created_at,updated_at";
export const nodeColumns = "id,space_id,roadmap_id,milestone_id,definition_id,node_kind,title,description,target_date::timestamp AT TIME ZONE 'UTC' AS target_date,position_x,position_y,field_values,version,archived_at,created_at,updated_at";
