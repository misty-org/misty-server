import { z } from "zod";
export declare const IdentifierSchema: z.ZodString;
export declare const SpacePathSchema: z.ZodObject<{
    spaceID: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export declare const NotePathSchema: z.ZodObject<{
    spaceID: z.ZodOptional<z.ZodString>;
    noteID: z.ZodString;
}, z.core.$strict>;
export declare const DrawingPathSchema: z.ZodObject<{
    spaceID: z.ZodOptional<z.ZodString>;
    drawingID: z.ZodString;
}, z.core.$strict>;
export declare const TaskPathSchema: z.ZodObject<{
    spaceID: z.ZodOptional<z.ZodString>;
    taskID: z.ZodString;
}, z.core.$strict>;
export declare const RoadmapPathSchema: z.ZodObject<{
    spaceID: z.ZodOptional<z.ZodString>;
    roadmapID: z.ZodString;
}, z.core.$strict>;
export declare const EventPathSchema: z.ZodObject<{
    spaceID: z.ZodOptional<z.ZodString>;
    eventID: z.ZodString;
}, z.core.$strict>;
export declare const EmptyParamsSchema: z.ZodObject<{
    path: z.ZodOptional<z.ZodObject<{
        spaceID: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const NoteParamsSchema: z.ZodObject<{
    path: z.ZodObject<{
        spaceID: z.ZodOptional<z.ZodString>;
        noteID: z.ZodString;
    }, z.core.$strict>;
}, z.core.$strict>;
export declare const DrawingParamsSchema: z.ZodObject<{
    path: z.ZodObject<{
        spaceID: z.ZodOptional<z.ZodString>;
        drawingID: z.ZodString;
    }, z.core.$strict>;
}, z.core.$strict>;
export declare const RoadmapParamsSchema: z.ZodObject<{
    path: z.ZodObject<{
        spaceID: z.ZodOptional<z.ZodString>;
        roadmapID: z.ZodString;
    }, z.core.$strict>;
}, z.core.$strict>;
export declare const VersionQuerySchema: z.ZodObject<{
    version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
}, z.core.$strict>;
export declare const TaskQuerySchema: z.ZodObject<{
    status: z.ZodOptional<z.ZodEnum<{
        todo: "todo";
        in_progress: "in_progress";
        done: "done";
        canceled: "canceled";
    }>>;
    priority: z.ZodOptional<z.ZodEnum<{
        high: "high";
        medium: "medium";
        low: "low";
    }>>;
    assignee_user_id: z.ZodOptional<z.ZodString>;
    assignee_agent_id: z.ZodOptional<z.ZodString>;
    q: z.ZodOptional<z.ZodString>;
    due_from: z.ZodOptional<z.ZodISODateTime>;
    due_to: z.ZodOptional<z.ZodISODateTime>;
    sort: z.ZodOptional<z.ZodEnum<{
        rank: "rank";
        due: "due";
        updated: "updated";
    }>>;
    cursor: z.ZodOptional<z.ZodString>;
    limit: z.ZodOptional<z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>>;
    include_archived: z.ZodOptional<z.ZodUnion<readonly [z.ZodBoolean, z.ZodEnum<{
        true: "true";
        false: "false";
    }>]>>;
}, z.core.$strict>;
export declare const CalendarQuerySchema: z.ZodObject<{
    from: z.ZodISODateTime;
    to: z.ZodISODateTime;
}, z.core.$strict>;
export declare const TitleInputSchema: z.ZodObject<{
    title: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export declare const NoteMetadataInputSchema: z.ZodObject<{
    shared_tags: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
}, z.core.$strict>;
export declare const TaskCreateInputSchema: z.ZodObject<{
    title: z.ZodString;
    notes: z.ZodOptional<z.ZodString>;
    status: z.ZodOptional<z.ZodEnum<{
        "": "";
        todo: "todo";
        in_progress: "in_progress";
        done: "done";
        canceled: "canceled";
    }>>;
    priority: z.ZodOptional<z.ZodEnum<{
        "": "";
        high: "high";
        medium: "medium";
        low: "low";
    }>>;
    assignee_user_id: z.ZodOptional<z.ZodString>;
    assignee_agent_id: z.ZodOptional<z.ZodString>;
    due_at: z.ZodOptional<z.ZodNullable<z.ZodISODateTime>>;
    due_timezone: z.ZodOptional<z.ZodString>;
    source_refs: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
        kind: z.ZodString;
        resource_id: z.ZodString;
    }, z.core.$loose>>>>;
    agent_run: z.ZodOptional<z.ZodNullable<z.ZodObject<{
        mode: z.ZodOptional<z.ZodString>;
        context_references: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
            device_id: z.ZodString;
            kind: z.ZodString;
            opaque_ref: z.ZodString;
            display_name: z.ZodOptional<z.ZodString>;
            capabilities: z.ZodJSONSchema;
            metadata: z.ZodOptional<z.ZodJSONSchema>;
        }, z.core.$loose>>>>;
    }, z.core.$loose>>>;
}, z.core.$strict>;
export declare const TaskUpdateInputSchema: z.ZodObject<{
    id: z.ZodOptional<z.ZodString>;
    space_id: z.ZodOptional<z.ZodString>;
    task_number: z.ZodOptional<z.ZodNumber>;
    task_key: z.ZodOptional<z.ZodString>;
    rank: z.ZodOptional<z.ZodNumber>;
    created_by_user_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    created_by_agent_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    source_run_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    audience_kind: z.ZodOptional<z.ZodString>;
    audience_conversation_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    audience_creator_user_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    completed_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
    archived_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
    schedule: z.ZodOptional<z.ZodOptional<z.ZodJSONSchema>>;
    calendar: z.ZodOptional<z.ZodOptional<z.ZodJSONSchema>>;
    conflicted_fields: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>>;
    created_at: z.ZodOptional<z.ZodString>;
    updated_at: z.ZodOptional<z.ZodString>;
    version: z.ZodNumber;
    title: z.ZodString;
    notes: z.ZodOptional<z.ZodString>;
    status: z.ZodOptional<z.ZodEnum<{
        "": "";
        todo: "todo";
        in_progress: "in_progress";
        done: "done";
        canceled: "canceled";
    }>>;
    priority: z.ZodOptional<z.ZodEnum<{
        "": "";
        high: "high";
        medium: "medium";
        low: "low";
    }>>;
    assignee_user_id: z.ZodOptional<z.ZodString>;
    assignee_agent_id: z.ZodOptional<z.ZodString>;
    due_at: z.ZodOptional<z.ZodNullable<z.ZodISODateTime>>;
    due_timezone: z.ZodOptional<z.ZodString>;
    source_refs: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
        kind: z.ZodString;
        resource_id: z.ZodString;
    }, z.core.$loose>>>>;
    agent_run: z.ZodOptional<z.ZodNullable<z.ZodObject<{
        mode: z.ZodOptional<z.ZodString>;
        context_references: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
            device_id: z.ZodString;
            kind: z.ZodString;
            opaque_ref: z.ZodString;
            display_name: z.ZodOptional<z.ZodString>;
            capabilities: z.ZodJSONSchema;
            metadata: z.ZodOptional<z.ZodJSONSchema>;
        }, z.core.$loose>>>>;
    }, z.core.$loose>>>;
}, z.core.$loose>;
export declare const CalendarCreateInputSchema: z.ZodObject<{
    title: z.ZodString;
    description: z.ZodOptional<z.ZodString>;
    location: z.ZodOptional<z.ZodString>;
    starts_at: z.ZodISODateTime;
    ends_at: z.ZodISODateTime;
    all_day: z.ZodOptional<z.ZodBoolean>;
    timezone: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export declare const CalendarUpdateInputSchema: z.ZodObject<{
    id: z.ZodOptional<z.ZodString>;
    space_id: z.ZodOptional<z.ZodString>;
    source_id: z.ZodOptional<z.ZodString>;
    provider: z.ZodOptional<z.ZodString>;
    external_event_id: z.ZodOptional<z.ZodString>;
    fingerprint: z.ZodOptional<z.ZodString>;
    meeting_url: z.ZodOptional<z.ZodString>;
    organizer: z.ZodOptional<z.ZodJSONSchema>;
    status: z.ZodOptional<z.ZodString>;
    provider_created_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
    provider_updated_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
    removed_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
    created_at: z.ZodOptional<z.ZodString>;
    updated_at: z.ZodOptional<z.ZodString>;
    origin: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    audience_kind: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    audience_conversation_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    created_by_user_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    created_by_agent_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    source_run_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
    version: z.ZodNumber;
    title: z.ZodString;
    description: z.ZodOptional<z.ZodString>;
    location: z.ZodOptional<z.ZodString>;
    starts_at: z.ZodISODateTime;
    ends_at: z.ZodISODateTime;
    all_day: z.ZodOptional<z.ZodBoolean>;
    timezone: z.ZodOptional<z.ZodString>;
}, z.core.$loose>;
export declare const RoadmapCreateInputSchema: z.ZodObject<{
    name: z.ZodString;
    description: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export declare const RoadmapUpdateInputSchema: z.ZodObject<{
    name: z.ZodString;
    description: z.ZodOptional<z.ZodString>;
    expected_version: z.ZodNumber;
}, z.core.$strict>;
