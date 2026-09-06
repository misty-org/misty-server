import { z } from "zod";
import { SpaceTaskSchema, SpaceCalendarEventSchema, SpaceTaskAgentRunInputSchema, } from "./models.js";
export const IdentifierSchema = z.string().regex(/^[A-Za-z0-9_-]{1,256}$/);
export const SpacePathSchema = z.strictObject({
    spaceID: IdentifierSchema.optional(),
});
export const NotePathSchema = SpacePathSchema.extend({
    noteID: IdentifierSchema,
});
export const DrawingPathSchema = SpacePathSchema.extend({
    drawingID: IdentifierSchema,
});
export const TaskPathSchema = SpacePathSchema.extend({
    taskID: IdentifierSchema,
});
export const RoadmapPathSchema = SpacePathSchema.extend({
    roadmapID: IdentifierSchema,
});
export const EventPathSchema = SpacePathSchema.extend({
    eventID: IdentifierSchema,
});
export const EmptyParamsSchema = z.strictObject({
    path: SpacePathSchema.optional(),
});
export const NoteParamsSchema = z.strictObject({ path: NotePathSchema });
export const DrawingParamsSchema = z.strictObject({ path: DrawingPathSchema });
export const RoadmapParamsSchema = z.strictObject({ path: RoadmapPathSchema });
const timestamp = z.iso.datetime({ offset: true });
const version = z.union([
    z.number().int().positive(),
    z.string().regex(/^[1-9][0-9]*$/),
]);
export const VersionQuerySchema = z.strictObject({ version });
export const TaskQuerySchema = z.strictObject({
    status: z.enum(["todo", "in_progress", "done", "canceled"]).optional(),
    priority: z.enum(["high", "medium", "low"]).optional(),
    assignee_user_id: z.string().optional(),
    assignee_agent_id: z.string().optional(),
    q: z.string().optional(),
    due_from: timestamp.optional(),
    due_to: timestamp.optional(),
    sort: z.enum(["rank", "due", "updated"]).optional(),
    cursor: z.string().optional(),
    limit: z.union([z.number().int(), z.string().regex(/^-?[0-9]+$/)]).optional(),
    include_archived: z
        .union([z.boolean(), z.enum(["true", "false"])])
        .optional(),
});
export const CalendarQuerySchema = z
    .strictObject({ from: timestamp, to: timestamp })
    .refine(({ from, to }) => Date.parse(to) > Date.parse(from) &&
    Date.parse(to) - Date.parse(from) <= 370 * 86400000, "Invalid calendar interval");
export const TitleInputSchema = z.strictObject({
    title: z.string().optional(),
});
export const NoteMetadataInputSchema = z.strictObject({
    shared_tags: z.array(z.string()).nullable().optional(),
});
export const TaskCreateInputSchema = z.strictObject({
    title: z.string(),
    notes: z.string().optional(),
    status: z.enum(["todo", "in_progress", "done", "canceled", ""]).optional(),
    priority: z.enum(["high", "medium", "low", ""]).optional(),
    assignee_user_id: z.string().optional(),
    assignee_agent_id: z.string().optional(),
    due_at: timestamp.nullable().optional(),
    due_timezone: z.string().optional(),
    source_refs: z
        .array(z.looseObject({ kind: z.string(), resource_id: z.string() }))
        .nullable()
        .optional(),
    agent_run: SpaceTaskAgentRunInputSchema.nullable().optional(),
});
// Existing clients send the complete current task/event on PATCH. Preserve those fields.
export const TaskUpdateInputSchema = SpaceTaskSchema.partial().extend({
    ...TaskCreateInputSchema.shape,
    version: z.number().int().positive(),
});
export const CalendarCreateInputSchema = z.strictObject({
    title: z.string(),
    description: z.string().optional(),
    location: z.string().optional(),
    starts_at: timestamp,
    ends_at: timestamp,
    all_day: z.boolean().optional(),
    timezone: z.string().optional(),
});
export const CalendarUpdateInputSchema = SpaceCalendarEventSchema.partial().extend({
    ...CalendarCreateInputSchema.shape,
    version: z.number().int().positive(),
});
export const RoadmapCreateInputSchema = z.strictObject({
    name: z.string(),
    description: z.string().optional(),
});
export const RoadmapUpdateInputSchema = RoadmapCreateInputSchema.extend({
    expected_version: z.number().int().positive(),
});
//# sourceMappingURL=requests.js.map