import { z } from "zod";
// Wire schemas preserve Go JSON names, omitted fields and nullable slices/maps.
// Additive response fields are retained for forward compatibility.
// spaces_limits.go
export const SpaceSchema = z.looseObject({
    id: z.string(),
    security_domain_id: z.string(),
    owner_user_id: z.string(),
    name: z.string(),
    is_default: z.boolean(),
    role: z.string(),
    member_count: z.number().int(),
    pending_count: z.number().int(),
    is_shared: z.boolean(),
    permissions: z.record(z.string(), z.boolean()).nullable(),
    created_at: z.string(),
    updated_at: z.string(),
});
// spaces_limits.go
export const SpaceMemberSchema = z.looseObject({
    space_id: z.string(),
    user_id: z.string(),
    name: z.string(),
    email: z.string(),
    role: z.string(),
    joined_at: z.string(),
    read_message_seq: z.number().int(),
});
// space_notes_note_lifecycle_active.go
export const SpaceNoteSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    creator_user_id: z.string(),
    title: z.string(),
    markdown: z.string().optional(),
    plain_text: z.string().optional(),
    lifecycle_state: z.string(),
    collaboration_revision: z.number().int(),
    acl_version: z.number().int(),
    audience_kind: z.string(),
    audience_conversation_id: z.string().optional(),
    created_at: z.string(),
    updated_at: z.string(),
    role: z.enum(["creator", "editor", "viewer"]),
    can_delete: z.boolean(),
    backlink_count: z.number().int(),
});
// space_note_projections.go
export const SpaceNoteBacklinkSchema = z.looseObject({
    id: z.string(),
    title: z.string(),
    updated_at: z.string(),
});
// space_drawings_drawing_lifecycle_active.go
export const SpaceDrawingSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    creator_user_id: z.string(),
    title: z.string(),
    lifecycle_state: z.string(),
    collaboration_revision: z.number().int(),
    acl_version: z.number().int(),
    created_at: z.string(),
    updated_at: z.string(),
    role: z.enum(["creator", "editor", "viewer"]),
    can_delete: z.boolean(),
    audience_kind: z.string(),
    audience_conversation_id: z.string().optional(),
});
// creator_agent_runs.go
export const CreatorAgentContextReferenceSchema = z.looseObject({
    device_id: z.string(),
    kind: z.string(),
    opaque_ref: z.string(),
    display_name: z.string().optional(),
    capabilities: z.json(),
    metadata: z.json().optional(),
});
// space_tasks_space_task.go
export const SpaceTaskAgentRunInputSchema = z.looseObject({
    mode: z.string().optional(),
    context_references: z
        .array(CreatorAgentContextReferenceSchema)
        .nullable()
        .optional(),
});
// space_tasks_space_task.go
export const SpaceTaskSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    task_number: z.number().int(),
    task_key: z.string(),
    title: z.string(),
    notes: z.string(),
    status: z.string(),
    priority: z.string(),
    rank: z.number().int(),
    assignee_user_id: z.string().optional(),
    assignee_agent_id: z.string().optional(),
    agent_run: SpaceTaskAgentRunInputSchema.nullable().optional(),
    due_at: z.string().nullable().optional(),
    due_timezone: z.string(),
    source_refs: z.json(),
    created_by_user_id: z.string().optional(),
    created_by_agent_id: z.string().optional(),
    source_run_id: z.string().optional(),
    audience_kind: z.string(),
    audience_conversation_id: z.string().optional(),
    audience_creator_user_id: z.string().optional(),
    version: z.number().int(),
    completed_at: z.string().nullable().optional(),
    archived_at: z.string().nullable().optional(),
    schedule: z.json().optional(),
    calendar: z.json().optional(),
    conflicted_fields: z.array(z.string()).nullable().optional(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_tasks_space_task.go
export const SpaceTaskPageSchema = z.looseObject({
    tasks: z.array(SpaceTaskSchema).nullable(),
    next_cursor: z.string().optional(),
    status_totals: z.record(z.string(), z.number().int()).nullable(),
});
// space_tasks_space_task.go
export const SpaceCalendarEventSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    source_id: z.string(),
    provider: z.string(),
    external_event_id: z.string(),
    fingerprint: z.string(),
    title: z.string(),
    description: z.string(),
    location: z.string(),
    meeting_url: z.string(),
    organizer: z.json(),
    starts_at: z.string(),
    ends_at: z.string(),
    all_day: z.boolean(),
    timezone: z.string(),
    status: z.string(),
    provider_created_at: z.string().nullable().optional(),
    provider_updated_at: z.string().nullable().optional(),
    removed_at: z.string().nullable().optional(),
    created_at: z.string(),
    updated_at: z.string(),
    origin: z.string().optional(),
    version: z.number().int().optional(),
    audience_kind: z.string().optional(),
    audience_conversation_id: z.string().optional(),
    created_by_user_id: z.string().optional(),
    created_by_agent_id: z.string().optional(),
    source_run_id: z.string().optional(),
});
// space_roadmaps.go
export const SpaceRoadmapSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    name: z.string(),
    description: z.string(),
    graph_version: z.number().int(),
    created_by_user_id: z.string(),
    audience_kind: z.string(),
    audience_conversation_id: z.string().optional(),
    archived_at: z.string().nullable().optional(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapMilestoneSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    roadmap_id: z.string(),
    title: z.string(),
    description: z.string(),
    target_date: z.string().nullable().optional(),
    rank: z.number().int(),
    position_x: z.number(),
    position_y: z.number(),
    width: z.number(),
    height: z.number(),
    version: z.number().int(),
    goal_total: z.number().int(),
    goal_done: z.number().int(),
    status: z.string(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapGoalSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    roadmap_id: z.string(),
    milestone_id: z.string(),
    title: z.string(),
    description: z.string(),
    target_date: z.string().nullable().optional(),
    rank: z.number().int(),
    position_x: z.number(),
    position_y: z.number(),
    manual_completed_at: z.string().nullable().optional(),
    manual_completed_by_user_id: z.string().optional(),
    version: z.number().int(),
    task_total: z.number().int(),
    task_done: z.number().int(),
    progress_percentage: z.number().int(),
    status: z.string(),
    tasks: z.array(SpaceTaskSchema).nullable(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapNodeSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    roadmap_id: z.string(),
    milestone_id: z.string().optional(),
    definition_id: z.string().optional(),
    node_kind: z.string(),
    title: z.string(),
    description: z.string(),
    target_date: z.string().nullable().optional(),
    position_x: z.number(),
    position_y: z.number(),
    field_values: z.json(),
    version: z.number().int(),
    archived_at: z.string().nullable().optional(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapNodeDefinitionSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    name: z.string(),
    description: z.string(),
    icon: z.string(),
    color: z.string(),
    agenda_visible: z.boolean(),
    field_schema: z.json(),
    version: z.number().int(),
    created_by_user_id: z.string(),
    archived_at: z.string().nullable().optional(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapEdgeEndpointSchema = z.looseObject({
    kind: z.string(),
    id: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapEdgeSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    roadmap_id: z.string(),
    source: SpaceRoadmapEdgeEndpointSchema,
    target: SpaceRoadmapEdgeEndpointSchema,
    source_goal_id: z.string().optional(),
    target_goal_id: z.string().optional(),
    edge_type: z.string(),
    label: z.string(),
    version: z.number().int(),
    created_at: z.string(),
    updated_at: z.string(),
});
// space_roadmaps.go
export const SpaceRoadmapSnapshotSchema = z.looseObject({
    roadmap: SpaceRoadmapSchema,
    milestones: z.array(SpaceRoadmapMilestoneSchema).nullable(),
    goals: z.array(SpaceRoadmapGoalSchema).nullable(),
    nodes: z.array(SpaceRoadmapNodeSchema).nullable(),
    node_definitions: z.array(SpaceRoadmapNodeDefinitionSchema).nullable(),
    edges: z.array(SpaceRoadmapEdgeSchema).nullable(),
    goal_total: z.number().int(),
    goal_done: z.number().int(),
    milestone_total: z.number().int(),
    milestone_done: z.number().int(),
    progress_percentage: z.number().int(),
});
// journal_collab_config.go
export const JournalTicketSchema = z.looseObject({
    ticket: z.string(),
    room: z.string(),
    url: z.string(),
    role: z.string(),
    expires_at: z.string(),
});
export const SpaceAgentMembershipSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    agent_id: z.string(),
    owner_user_id: z.string(),
    can_control: z.boolean(),
    name: z.string(),
    description: z.string(),
    icon: z.string(),
    avatar: z.json(),
    instructions: z.string().optional(),
    model_id: z.string().optional(),
    reasoning_effort: z.string().optional(),
    default_run_mode: z.string(),
    enabled: z.boolean(),
    version: z.number().int(),
    created_at: z.string(),
    updated_at: z.string(),
    work_state: z.string(),
    attention_count: z.number().int(),
    last_activity_at: z.string().nullable().optional(),
    current_task_id: z.string().optional(),
});
export const SpaceTaskActivitySchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    task_id: z.string(),
    actor_kind: z.string(),
    actor_user_id: z.string().optional(),
    actor_agent_id: z.string().optional(),
    run_id: z.string().optional(),
    kind: z.string(),
    message: z.string(),
    metadata: z.json(),
    created_at: z.string(),
});
export const SpaceTaskMoveResultSchema = z.looseObject({
    task: SpaceTaskSchema,
    reordered: z.array(SpaceTaskSchema).nullable(),
});
export const SpaceAgendaEntrySchema = z.looseObject({
    id: z.string(),
    kind: z.string(),
    title: z.string(),
    description: z.string().optional(),
    starts_at: z.string(),
    ends_at: z.string(),
    all_day: z.boolean(),
    timezone: z.string(),
    status: z.string().optional(),
    source_id: z.string().optional(),
    task_id: z.string().optional(),
    roadmap_id: z.string().optional(),
    milestone_id: z.string().optional(),
    goal_id: z.string().optional(),
    roadmap_node_id: z.string().optional(),
    roadmap_node_kind: z.string().optional(),
    definition_id: z.string().optional(),
    meeting_url: z.string().optional(),
    location: z.string().optional(),
    external_event_id: z.string().optional(),
    version: z.number().int().optional(),
});
export const SpaceAgendaSnapshotSchema = z.looseObject({
    entries: z.array(SpaceAgendaEntrySchema).nullable(),
});
export const SpaceCalendarSourceSchema = z.looseObject({
    id: z.string(),
    space_id: z.string(),
    integration_id: z.string(),
    connected_by_user_id: z.string(),
    provider: z.string(),
    external_calendar_id: z.string(),
    display_name: z.string(),
    timezone: z.string(),
    watch_expires_at: z.string().nullable().optional(),
    status: z.string(),
    last_error_code: z.string().optional(),
    last_reconciled_at: z.string().nullable().optional(),
    disabled_at: z.string().nullable().optional(),
    created_at: z.string(),
    updated_at: z.string(),
});
export const SpaceRoadmapMutationResultSchema = z.looseObject({
    graph_version: z.number().int(),
});
//# sourceMappingURL=models.js.map