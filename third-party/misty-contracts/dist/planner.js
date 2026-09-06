import { z } from "zod";
import * as model from "./models.js";
import * as input from "./requests.js";
const graphVersion = z.number().int().positive();
const expected = { expected_version: graphVersion };
const versionQuery = z.strictObject({
    expected_version: z.union([graphVersion, z.string().regex(/^[1-9][0-9]*$/)]),
});
const space = { path: input.SpacePathSchema.optional() };
const roadmap = { path: input.RoadmapPathSchema };
const milestonePath = input.RoadmapPathSchema.extend({
    milestoneID: input.IdentifierSchema,
});
const goalPath = input.RoadmapPathSchema.extend({
    goalID: input.IdentifierSchema,
});
const nodePath = input.RoadmapPathSchema.extend({
    nodeID: input.IdentifierSchema,
});
const edgePath = input.RoadmapPathSchema.extend({
    edgeID: input.IdentifierSchema,
});
const definitionPath = input.SpacePathSchema.extend({
    definitionID: input.IdentifierSchema,
});
const milestoneInput = model.SpaceRoadmapMilestoneSchema.partial().extend({
    title: z.string(),
    ...expected,
});
const goalInput = model.SpaceRoadmapGoalSchema.partial().extend({
    title: z.string(),
    ...expected,
});
const nodeInput = model.SpaceRoadmapNodeSchema.partial().extend({
    title: z.string(),
    ...expected,
});
const edgeInput = model.SpaceRoadmapEdgeSchema.partial().extend(expected);
const definitionInput = model.SpaceRoadmapNodeDefinitionSchema.partial().extend({ name: z.string() });
const milestoneResult = z.looseObject({
    milestone: model.SpaceRoadmapMilestoneSchema,
    graph_version: graphVersion,
});
const goalResult = z.looseObject({
    goal: model.SpaceRoadmapGoalSchema,
    graph_version: graphVersion,
});
const nodeResult = z.looseObject({
    node: model.SpaceRoadmapNodeSchema,
    graph_version: graphVersion,
});
const edgeResult = z.looseObject({
    edge: model.SpaceRoadmapEdgeSchema,
    graph_version: graphVersion,
});
export const GoogleCalendarChoiceSchema = z.looseObject({
    id: z.string(),
    summary: z.string(),
    timeZone: z.string(),
    primary: z.boolean(),
    accessRole: z.string(),
});
/** Planner operations preserve the existing HTTP version checks and JSON response shapes. */
export const mistyPlannerContracts = {
    "tasks.activity.list": {
        verb: "GET",
        path: "/spaces/{spaceID}/tasks/{taskID}/activity",
        params: z.strictObject({ path: input.TaskPathSchema }),
        result: z.looseObject({
            activity: z.array(model.SpaceTaskActivitySchema).nullable(),
        }),
    },
    "tasks.move": {
        verb: "POST",
        path: "/spaces/{spaceID}/tasks/{taskID}/move",
        params: z.strictObject({
            path: input.TaskPathSchema,
            body: z.strictObject({
                version: graphVersion,
                status: z.enum(["todo", "in_progress", "done", "canceled"]),
                before_task_id: z.string().optional(),
            }),
        }),
        result: model.SpaceTaskMoveResultSchema,
    },
    "agenda.list": {
        verb: "GET",
        path: "/spaces/{spaceID}/agenda",
        params: z.strictObject({ ...space, query: input.CalendarQuerySchema }),
        result: model.SpaceAgendaSnapshotSchema,
    },
    "calendar.sources.list": {
        verb: "GET",
        path: "/spaces/{spaceID}/calendar/sources",
        params: input.EmptyParamsSchema,
        result: z.looseObject({
            sources: z.array(model.SpaceCalendarSourceSchema).nullable(),
        }),
    },
    "calendar.sources.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/calendar/sources",
        params: z.strictObject({
            ...space,
            body: z.strictObject({
                integration_id: input.IdentifierSchema,
                external_calendar_id: z.string().min(1),
                display_name: z.string().optional(),
                timezone: z.string().optional(),
            }),
        }),
        result: model.SpaceCalendarSourceSchema,
    },
    "calendar.sources.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/calendar/sources/{sourceID}",
        params: z.strictObject({
            path: input.SpacePathSchema.extend({ sourceID: input.IdentifierSchema }),
        }),
        result: z.undefined(),
    },
    "calendar.google.calendars": {
        verb: "GET",
        path: "/spaces/{spaceID}/calendar/google/calendars",
        params: z.strictObject({
            ...space,
            query: z.strictObject({ integration_id: input.IdentifierSchema }),
        }),
        result: z.looseObject({
            calendars: z.array(GoogleCalendarChoiceSchema).nullable(),
        }),
    },
    "calendar.sync": {
        verb: "POST",
        path: "/spaces/{spaceID}/calendar/sync",
        params: z.strictObject({
            ...space,
            body: z.strictObject({ source_id: z.string().optional() }),
        }),
        result: z.looseObject({
            tasks: z.array(model.SpaceTaskSchema).nullable(),
            sources: z.array(model.SpaceCalendarSourceSchema).nullable(),
            synced_at: z.string(),
        }),
    },
    "roadmaps.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}",
        params: z.strictObject({ ...roadmap, query: versionQuery }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
    "roadmaps.nodeDefinitions.list": {
        verb: "GET",
        path: "/spaces/{spaceID}/roadmap-node-definitions",
        params: input.EmptyParamsSchema,
        result: z.looseObject({
            node_definitions: z
                .array(model.SpaceRoadmapNodeDefinitionSchema)
                .nullable(),
        }),
    },
    "roadmaps.nodeDefinitions.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/roadmap-node-definitions",
        params: z.strictObject({ ...space, body: definitionInput }),
        result: model.SpaceRoadmapNodeDefinitionSchema,
    },
    "roadmaps.nodeDefinitions.update": {
        verb: "PATCH",
        path: "/spaces/{spaceID}/roadmap-node-definitions/{definitionID}",
        params: z.strictObject({
            path: definitionPath,
            body: definitionInput.extend(expected),
        }),
        result: model.SpaceRoadmapNodeDefinitionSchema,
    },
    "roadmaps.nodeDefinitions.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/roadmap-node-definitions/{definitionID}",
        params: z.strictObject({ path: definitionPath, query: versionQuery }),
        result: z.undefined(),
    },
    "roadmaps.milestones.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/milestones",
        params: z.strictObject({ ...roadmap, body: milestoneInput }),
        result: milestoneResult,
    },
    "roadmaps.milestones.update": {
        verb: "PATCH",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/milestones/{milestoneID}",
        params: z.strictObject({ path: milestonePath, body: milestoneInput }),
        result: milestoneResult,
    },
    "roadmaps.milestones.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/milestones/{milestoneID}",
        params: z.strictObject({ path: milestonePath, query: versionQuery }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
    "roadmaps.goals.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals",
        params: z.strictObject({ ...roadmap, body: goalInput }),
        result: goalResult,
    },
    "roadmaps.goals.update": {
        verb: "PATCH",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}",
        params: z.strictObject({
            path: goalPath,
            body: goalInput.extend({
                complete_manually: z.boolean().nullable().optional(),
            }),
        }),
        result: goalResult,
    },
    "roadmaps.goals.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}",
        params: z.strictObject({ path: goalPath, query: versionQuery }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
    "roadmaps.goals.setTasks": {
        verb: "PUT",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}/tasks",
        params: z.strictObject({
            path: goalPath,
            body: z.strictObject({
                task_ids: z.array(input.IdentifierSchema).nullable(),
                ...expected,
            }),
        }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
    "roadmaps.nodes.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/nodes",
        params: z.strictObject({ ...roadmap, body: nodeInput }),
        result: nodeResult,
    },
    "roadmaps.nodes.update": {
        verb: "PATCH",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}",
        params: z.strictObject({ path: nodePath, body: nodeInput }),
        result: nodeResult,
    },
    "roadmaps.nodes.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}",
        params: z.strictObject({ path: nodePath, query: versionQuery }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
    "roadmaps.edges.create": {
        verb: "POST",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/edges",
        params: z.strictObject({ ...roadmap, body: edgeInput }),
        result: edgeResult,
    },
    "roadmaps.edges.update": {
        verb: "PATCH",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/edges/{edgeID}",
        params: z.strictObject({ path: edgePath, body: edgeInput }),
        result: edgeResult,
    },
    "roadmaps.edges.delete": {
        verb: "DELETE",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/edges/{edgeID}",
        params: z.strictObject({ path: edgePath, query: versionQuery }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
    "roadmaps.layout.update": {
        verb: "PATCH",
        path: "/spaces/{spaceID}/roadmaps/{roadmapID}/layout",
        params: z.strictObject({
            ...roadmap,
            body: z.strictObject({
                ...expected,
                milestones: z
                    .array(model.SpaceRoadmapMilestoneSchema.partial().extend({
                    id: input.IdentifierSchema,
                }))
                    .nullable()
                    .optional(),
                goals: z
                    .array(model.SpaceRoadmapGoalSchema.partial().extend({
                    id: input.IdentifierSchema,
                }))
                    .nullable()
                    .optional(),
                nodes: z
                    .array(model.SpaceRoadmapNodeSchema.partial().extend({
                    id: input.IdentifierSchema,
                }))
                    .nullable()
                    .optional(),
            }),
        }),
        result: model.SpaceRoadmapMutationResultSchema,
    },
};
//# sourceMappingURL=planner.js.map