import { z } from "zod";
export declare const GoogleCalendarChoiceSchema: z.ZodObject<{
    id: z.ZodString;
    summary: z.ZodString;
    timeZone: z.ZodString;
    primary: z.ZodBoolean;
    accessRole: z.ZodString;
}, z.core.$loose>;
export type GoogleCalendarChoice = z.output<typeof GoogleCalendarChoiceSchema>;
/** Planner operations preserve the existing HTTP version checks and JSON response shapes. */
export declare const mistyPlannerContracts: {
    readonly "tasks.activity.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/tasks/{taskID}/activity";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                taskID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            activity: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                task_id: z.ZodString;
                actor_kind: z.ZodString;
                actor_user_id: z.ZodOptional<z.ZodString>;
                actor_agent_id: z.ZodOptional<z.ZodString>;
                run_id: z.ZodOptional<z.ZodString>;
                kind: z.ZodString;
                message: z.ZodString;
                metadata: z.ZodJSONSchema;
                created_at: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "tasks.move": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/tasks/{taskID}/move";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                taskID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                version: z.ZodNumber;
                status: z.ZodEnum<{
                    todo: "todo";
                    in_progress: "in_progress";
                    done: "done";
                    canceled: "canceled";
                }>;
                before_task_id: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            task: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                task_number: z.ZodNumber;
                task_key: z.ZodString;
                title: z.ZodString;
                notes: z.ZodString;
                status: z.ZodString;
                priority: z.ZodString;
                rank: z.ZodNumber;
                assignee_user_id: z.ZodOptional<z.ZodString>;
                due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                due_timezone: z.ZodString;
                source_refs: z.ZodJSONSchema;
                created_by_user_id: z.ZodOptional<z.ZodString>;
                created_by_agent_id: z.ZodOptional<z.ZodString>;
                source_run_id: z.ZodOptional<z.ZodString>;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                audience_creator_user_id: z.ZodOptional<z.ZodString>;
                version: z.ZodNumber;
                completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                schedule: z.ZodOptional<z.ZodJSONSchema>;
                calendar: z.ZodOptional<z.ZodJSONSchema>;
                conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            reordered: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                task_number: z.ZodNumber;
                task_key: z.ZodString;
                title: z.ZodString;
                notes: z.ZodString;
                status: z.ZodString;
                priority: z.ZodString;
                rank: z.ZodNumber;
                assignee_user_id: z.ZodOptional<z.ZodString>;
                due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                due_timezone: z.ZodString;
                source_refs: z.ZodJSONSchema;
                created_by_user_id: z.ZodOptional<z.ZodString>;
                created_by_agent_id: z.ZodOptional<z.ZodString>;
                source_run_id: z.ZodOptional<z.ZodString>;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                audience_creator_user_id: z.ZodOptional<z.ZodString>;
                version: z.ZodNumber;
                completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                schedule: z.ZodOptional<z.ZodJSONSchema>;
                calendar: z.ZodOptional<z.ZodJSONSchema>;
                conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "agenda.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/agenda";
        readonly params: z.ZodObject<{
            query: z.ZodObject<{
                from: z.ZodISODateTime;
                to: z.ZodISODateTime;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            entries: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                kind: z.ZodString;
                title: z.ZodString;
                description: z.ZodOptional<z.ZodString>;
                starts_at: z.ZodString;
                ends_at: z.ZodString;
                all_day: z.ZodBoolean;
                timezone: z.ZodString;
                status: z.ZodOptional<z.ZodString>;
                source_id: z.ZodOptional<z.ZodString>;
                task_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                milestone_id: z.ZodOptional<z.ZodString>;
                goal_id: z.ZodOptional<z.ZodString>;
                roadmap_node_id: z.ZodOptional<z.ZodString>;
                roadmap_node_kind: z.ZodOptional<z.ZodString>;
                definition_id: z.ZodOptional<z.ZodString>;
                meeting_url: z.ZodOptional<z.ZodString>;
                location: z.ZodOptional<z.ZodString>;
                external_event_id: z.ZodOptional<z.ZodString>;
                version: z.ZodOptional<z.ZodNumber>;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "calendar.sources.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/calendar/sources";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            sources: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                integration_id: z.ZodString;
                connected_by_user_id: z.ZodString;
                provider: z.ZodString;
                external_calendar_id: z.ZodString;
                display_name: z.ZodString;
                timezone: z.ZodString;
                watch_expires_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                status: z.ZodString;
                last_error_code: z.ZodOptional<z.ZodString>;
                last_reconciled_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                disabled_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "calendar.sources.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/calendar/sources";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                integration_id: z.ZodString;
                external_calendar_id: z.ZodString;
                display_name: z.ZodOptional<z.ZodString>;
                timezone: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            integration_id: z.ZodString;
            connected_by_user_id: z.ZodString;
            provider: z.ZodString;
            external_calendar_id: z.ZodString;
            display_name: z.ZodString;
            timezone: z.ZodString;
            watch_expires_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            status: z.ZodString;
            last_error_code: z.ZodOptional<z.ZodString>;
            last_reconciled_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            disabled_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "calendar.sources.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/calendar/sources/{sourceID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                sourceID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "calendar.google.calendars": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/calendar/google/calendars";
        readonly params: z.ZodObject<{
            query: z.ZodObject<{
                integration_id: z.ZodString;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            calendars: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                summary: z.ZodString;
                timeZone: z.ZodString;
                primary: z.ZodBoolean;
                accessRole: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "calendar.sync": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/calendar/sync";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                source_id: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            tasks: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                task_number: z.ZodNumber;
                task_key: z.ZodString;
                title: z.ZodString;
                notes: z.ZodString;
                status: z.ZodString;
                priority: z.ZodString;
                rank: z.ZodNumber;
                assignee_user_id: z.ZodOptional<z.ZodString>;
                due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                due_timezone: z.ZodString;
                source_refs: z.ZodJSONSchema;
                created_by_user_id: z.ZodOptional<z.ZodString>;
                created_by_agent_id: z.ZodOptional<z.ZodString>;
                source_run_id: z.ZodOptional<z.ZodString>;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                audience_creator_user_id: z.ZodOptional<z.ZodString>;
                version: z.ZodNumber;
                completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                schedule: z.ZodOptional<z.ZodJSONSchema>;
                calendar: z.ZodOptional<z.ZodJSONSchema>;
                conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
            sources: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                integration_id: z.ZodString;
                connected_by_user_id: z.ZodString;
                provider: z.ZodString;
                external_calendar_id: z.ZodString;
                display_name: z.ZodString;
                timezone: z.ZodString;
                watch_expires_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                status: z.ZodString;
                last_error_code: z.ZodOptional<z.ZodString>;
                last_reconciled_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                disabled_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
            synced_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "roadmaps.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}";
        readonly params: z.ZodObject<{
            query: z.ZodObject<{
                expected_version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodeDefinitions.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/roadmap-node-definitions";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            node_definitions: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                name: z.ZodString;
                description: z.ZodString;
                icon: z.ZodString;
                color: z.ZodString;
                agenda_visible: z.ZodBoolean;
                field_schema: z.ZodJSONSchema;
                version: z.ZodNumber;
                created_by_user_id: z.ZodString;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodeDefinitions.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/roadmap-node-definitions";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                icon: z.ZodOptional<z.ZodString>;
                color: z.ZodOptional<z.ZodString>;
                agenda_visible: z.ZodOptional<z.ZodBoolean>;
                field_schema: z.ZodOptional<z.ZodJSONSchema>;
                version: z.ZodOptional<z.ZodNumber>;
                created_by_user_id: z.ZodOptional<z.ZodString>;
                archived_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                name: z.ZodString;
            }, z.core.$loose>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            name: z.ZodString;
            description: z.ZodString;
            icon: z.ZodString;
            color: z.ZodString;
            agenda_visible: z.ZodBoolean;
            field_schema: z.ZodJSONSchema;
            version: z.ZodNumber;
            created_by_user_id: z.ZodString;
            archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodeDefinitions.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmap-node-definitions/{definitionID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                definitionID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                icon: z.ZodOptional<z.ZodString>;
                color: z.ZodOptional<z.ZodString>;
                agenda_visible: z.ZodOptional<z.ZodBoolean>;
                field_schema: z.ZodOptional<z.ZodJSONSchema>;
                version: z.ZodOptional<z.ZodNumber>;
                created_by_user_id: z.ZodOptional<z.ZodString>;
                archived_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                name: z.ZodString;
                expected_version: z.ZodNumber;
            }, z.core.$loose>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            name: z.ZodString;
            description: z.ZodString;
            icon: z.ZodString;
            color: z.ZodString;
            agenda_visible: z.ZodBoolean;
            field_schema: z.ZodJSONSchema;
            version: z.ZodNumber;
            created_by_user_id: z.ZodString;
            archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodeDefinitions.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/roadmap-node-definitions/{definitionID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                definitionID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                expected_version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "roadmaps.milestones.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/milestones";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                rank: z.ZodOptional<z.ZodNumber>;
                position_x: z.ZodOptional<z.ZodNumber>;
                position_y: z.ZodOptional<z.ZodNumber>;
                width: z.ZodOptional<z.ZodNumber>;
                height: z.ZodOptional<z.ZodNumber>;
                version: z.ZodOptional<z.ZodNumber>;
                goal_total: z.ZodOptional<z.ZodNumber>;
                goal_done: z.ZodOptional<z.ZodNumber>;
                status: z.ZodOptional<z.ZodString>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
                title: z.ZodString;
            }, z.core.$loose>;
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            milestone: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                target_date: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                rank: z.ZodNumber;
                position_x: z.ZodNumber;
                position_y: z.ZodNumber;
                width: z.ZodNumber;
                height: z.ZodNumber;
                version: z.ZodNumber;
                goal_total: z.ZodNumber;
                goal_done: z.ZodNumber;
                status: z.ZodString;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.milestones.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/milestones/{milestoneID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                milestoneID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                rank: z.ZodOptional<z.ZodNumber>;
                position_x: z.ZodOptional<z.ZodNumber>;
                position_y: z.ZodOptional<z.ZodNumber>;
                width: z.ZodOptional<z.ZodNumber>;
                height: z.ZodOptional<z.ZodNumber>;
                version: z.ZodOptional<z.ZodNumber>;
                goal_total: z.ZodOptional<z.ZodNumber>;
                goal_done: z.ZodOptional<z.ZodNumber>;
                status: z.ZodOptional<z.ZodString>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
                title: z.ZodString;
            }, z.core.$loose>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            milestone: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                target_date: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                rank: z.ZodNumber;
                position_x: z.ZodNumber;
                position_y: z.ZodNumber;
                width: z.ZodNumber;
                height: z.ZodNumber;
                version: z.ZodNumber;
                goal_total: z.ZodNumber;
                goal_done: z.ZodNumber;
                status: z.ZodString;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.milestones.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/milestones/{milestoneID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                milestoneID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                expected_version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.goals.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                milestone_id: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                rank: z.ZodOptional<z.ZodNumber>;
                position_x: z.ZodOptional<z.ZodNumber>;
                position_y: z.ZodOptional<z.ZodNumber>;
                manual_completed_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                manual_completed_by_user_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                version: z.ZodOptional<z.ZodNumber>;
                task_total: z.ZodOptional<z.ZodNumber>;
                task_done: z.ZodOptional<z.ZodNumber>;
                progress_percentage: z.ZodOptional<z.ZodNumber>;
                status: z.ZodOptional<z.ZodString>;
                tasks: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                    id: z.ZodString;
                    space_id: z.ZodString;
                    task_number: z.ZodNumber;
                    task_key: z.ZodString;
                    title: z.ZodString;
                    notes: z.ZodString;
                    status: z.ZodString;
                    priority: z.ZodString;
                    rank: z.ZodNumber;
                    assignee_user_id: z.ZodOptional<z.ZodString>;
                    due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    due_timezone: z.ZodString;
                    source_refs: z.ZodJSONSchema;
                    created_by_user_id: z.ZodOptional<z.ZodString>;
                    created_by_agent_id: z.ZodOptional<z.ZodString>;
                    source_run_id: z.ZodOptional<z.ZodString>;
                    audience_kind: z.ZodString;
                    audience_conversation_id: z.ZodOptional<z.ZodString>;
                    audience_creator_user_id: z.ZodOptional<z.ZodString>;
                    version: z.ZodNumber;
                    completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    schedule: z.ZodOptional<z.ZodJSONSchema>;
                    calendar: z.ZodOptional<z.ZodJSONSchema>;
                    conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                    created_at: z.ZodString;
                    updated_at: z.ZodString;
                }, z.core.$loose>>>>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
                title: z.ZodString;
            }, z.core.$loose>;
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            goal: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                milestone_id: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                target_date: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                rank: z.ZodNumber;
                position_x: z.ZodNumber;
                position_y: z.ZodNumber;
                manual_completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                manual_completed_by_user_id: z.ZodOptional<z.ZodString>;
                version: z.ZodNumber;
                task_total: z.ZodNumber;
                task_done: z.ZodNumber;
                progress_percentage: z.ZodNumber;
                status: z.ZodString;
                tasks: z.ZodNullable<z.ZodArray<z.ZodObject<{
                    id: z.ZodString;
                    space_id: z.ZodString;
                    task_number: z.ZodNumber;
                    task_key: z.ZodString;
                    title: z.ZodString;
                    notes: z.ZodString;
                    status: z.ZodString;
                    priority: z.ZodString;
                    rank: z.ZodNumber;
                    assignee_user_id: z.ZodOptional<z.ZodString>;
                    due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    due_timezone: z.ZodString;
                    source_refs: z.ZodJSONSchema;
                    created_by_user_id: z.ZodOptional<z.ZodString>;
                    created_by_agent_id: z.ZodOptional<z.ZodString>;
                    source_run_id: z.ZodOptional<z.ZodString>;
                    audience_kind: z.ZodString;
                    audience_conversation_id: z.ZodOptional<z.ZodString>;
                    audience_creator_user_id: z.ZodOptional<z.ZodString>;
                    version: z.ZodNumber;
                    completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    schedule: z.ZodOptional<z.ZodJSONSchema>;
                    calendar: z.ZodOptional<z.ZodJSONSchema>;
                    conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                    created_at: z.ZodString;
                    updated_at: z.ZodString;
                }, z.core.$loose>>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.goals.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                goalID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                milestone_id: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                rank: z.ZodOptional<z.ZodNumber>;
                position_x: z.ZodOptional<z.ZodNumber>;
                position_y: z.ZodOptional<z.ZodNumber>;
                manual_completed_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                manual_completed_by_user_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                version: z.ZodOptional<z.ZodNumber>;
                task_total: z.ZodOptional<z.ZodNumber>;
                task_done: z.ZodOptional<z.ZodNumber>;
                progress_percentage: z.ZodOptional<z.ZodNumber>;
                status: z.ZodOptional<z.ZodString>;
                tasks: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                    id: z.ZodString;
                    space_id: z.ZodString;
                    task_number: z.ZodNumber;
                    task_key: z.ZodString;
                    title: z.ZodString;
                    notes: z.ZodString;
                    status: z.ZodString;
                    priority: z.ZodString;
                    rank: z.ZodNumber;
                    assignee_user_id: z.ZodOptional<z.ZodString>;
                    due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    due_timezone: z.ZodString;
                    source_refs: z.ZodJSONSchema;
                    created_by_user_id: z.ZodOptional<z.ZodString>;
                    created_by_agent_id: z.ZodOptional<z.ZodString>;
                    source_run_id: z.ZodOptional<z.ZodString>;
                    audience_kind: z.ZodString;
                    audience_conversation_id: z.ZodOptional<z.ZodString>;
                    audience_creator_user_id: z.ZodOptional<z.ZodString>;
                    version: z.ZodNumber;
                    completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    schedule: z.ZodOptional<z.ZodJSONSchema>;
                    calendar: z.ZodOptional<z.ZodJSONSchema>;
                    conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                    created_at: z.ZodString;
                    updated_at: z.ZodString;
                }, z.core.$loose>>>>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
                title: z.ZodString;
                complete_manually: z.ZodOptional<z.ZodNullable<z.ZodBoolean>>;
            }, z.core.$loose>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            goal: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                milestone_id: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                target_date: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                rank: z.ZodNumber;
                position_x: z.ZodNumber;
                position_y: z.ZodNumber;
                manual_completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                manual_completed_by_user_id: z.ZodOptional<z.ZodString>;
                version: z.ZodNumber;
                task_total: z.ZodNumber;
                task_done: z.ZodNumber;
                progress_percentage: z.ZodNumber;
                status: z.ZodString;
                tasks: z.ZodNullable<z.ZodArray<z.ZodObject<{
                    id: z.ZodString;
                    space_id: z.ZodString;
                    task_number: z.ZodNumber;
                    task_key: z.ZodString;
                    title: z.ZodString;
                    notes: z.ZodString;
                    status: z.ZodString;
                    priority: z.ZodString;
                    rank: z.ZodNumber;
                    assignee_user_id: z.ZodOptional<z.ZodString>;
                    due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    due_timezone: z.ZodString;
                    source_refs: z.ZodJSONSchema;
                    created_by_user_id: z.ZodOptional<z.ZodString>;
                    created_by_agent_id: z.ZodOptional<z.ZodString>;
                    source_run_id: z.ZodOptional<z.ZodString>;
                    audience_kind: z.ZodString;
                    audience_conversation_id: z.ZodOptional<z.ZodString>;
                    audience_creator_user_id: z.ZodOptional<z.ZodString>;
                    version: z.ZodNumber;
                    completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                    schedule: z.ZodOptional<z.ZodJSONSchema>;
                    calendar: z.ZodOptional<z.ZodJSONSchema>;
                    conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                    created_at: z.ZodString;
                    updated_at: z.ZodString;
                }, z.core.$loose>>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.goals.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                goalID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                expected_version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.goals.setTasks": {
        readonly verb: "PUT";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}/tasks";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                goalID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                expected_version: z.ZodNumber;
                task_ids: z.ZodNullable<z.ZodArray<z.ZodString>>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodes.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/nodes";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                milestone_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                definition_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                node_kind: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                position_x: z.ZodOptional<z.ZodNumber>;
                position_y: z.ZodOptional<z.ZodNumber>;
                field_values: z.ZodOptional<z.ZodJSONSchema>;
                version: z.ZodOptional<z.ZodNumber>;
                archived_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
                title: z.ZodString;
            }, z.core.$loose>;
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            node: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                milestone_id: z.ZodOptional<z.ZodString>;
                definition_id: z.ZodOptional<z.ZodString>;
                node_kind: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                target_date: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                position_x: z.ZodNumber;
                position_y: z.ZodNumber;
                field_values: z.ZodJSONSchema;
                version: z.ZodNumber;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodes.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                nodeID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                milestone_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                definition_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                node_kind: z.ZodOptional<z.ZodString>;
                description: z.ZodOptional<z.ZodString>;
                target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                position_x: z.ZodOptional<z.ZodNumber>;
                position_y: z.ZodOptional<z.ZodNumber>;
                field_values: z.ZodOptional<z.ZodJSONSchema>;
                version: z.ZodOptional<z.ZodNumber>;
                archived_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
                title: z.ZodString;
            }, z.core.$loose>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            node: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                milestone_id: z.ZodOptional<z.ZodString>;
                definition_id: z.ZodOptional<z.ZodString>;
                node_kind: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                target_date: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                position_x: z.ZodNumber;
                position_y: z.ZodNumber;
                field_values: z.ZodJSONSchema;
                version: z.ZodNumber;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.nodes.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                nodeID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                expected_version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.edges.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/edges";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                source: z.ZodOptional<z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>>;
                target: z.ZodOptional<z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>>;
                source_goal_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                target_goal_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                edge_type: z.ZodOptional<z.ZodString>;
                label: z.ZodOptional<z.ZodString>;
                version: z.ZodOptional<z.ZodNumber>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
            }, z.core.$loose>;
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            edge: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                source: z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>;
                target: z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>;
                source_goal_id: z.ZodOptional<z.ZodString>;
                target_goal_id: z.ZodOptional<z.ZodString>;
                edge_type: z.ZodString;
                label: z.ZodString;
                version: z.ZodNumber;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.edges.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/edges/{edgeID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                edgeID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                id: z.ZodOptional<z.ZodString>;
                space_id: z.ZodOptional<z.ZodString>;
                roadmap_id: z.ZodOptional<z.ZodString>;
                source: z.ZodOptional<z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>>;
                target: z.ZodOptional<z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>>;
                source_goal_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                target_goal_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                edge_type: z.ZodOptional<z.ZodString>;
                label: z.ZodOptional<z.ZodString>;
                version: z.ZodOptional<z.ZodNumber>;
                created_at: z.ZodOptional<z.ZodString>;
                updated_at: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
            }, z.core.$loose>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            edge: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                roadmap_id: z.ZodString;
                source: z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>;
                target: z.ZodObject<{
                    kind: z.ZodString;
                    id: z.ZodString;
                }, z.core.$loose>;
                source_goal_id: z.ZodOptional<z.ZodString>;
                target_goal_id: z.ZodOptional<z.ZodString>;
                edge_type: z.ZodString;
                label: z.ZodString;
                version: z.ZodNumber;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.edges.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/edges/{edgeID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
                edgeID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                expected_version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.layout.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}/layout";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                milestones: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                    space_id: z.ZodOptional<z.ZodString>;
                    roadmap_id: z.ZodOptional<z.ZodString>;
                    title: z.ZodOptional<z.ZodString>;
                    description: z.ZodOptional<z.ZodString>;
                    target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                    rank: z.ZodOptional<z.ZodNumber>;
                    position_x: z.ZodOptional<z.ZodNumber>;
                    position_y: z.ZodOptional<z.ZodNumber>;
                    width: z.ZodOptional<z.ZodNumber>;
                    height: z.ZodOptional<z.ZodNumber>;
                    version: z.ZodOptional<z.ZodNumber>;
                    goal_total: z.ZodOptional<z.ZodNumber>;
                    goal_done: z.ZodOptional<z.ZodNumber>;
                    status: z.ZodOptional<z.ZodString>;
                    created_at: z.ZodOptional<z.ZodString>;
                    updated_at: z.ZodOptional<z.ZodString>;
                    id: z.ZodString;
                }, z.core.$loose>>>>;
                goals: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                    space_id: z.ZodOptional<z.ZodString>;
                    roadmap_id: z.ZodOptional<z.ZodString>;
                    milestone_id: z.ZodOptional<z.ZodString>;
                    title: z.ZodOptional<z.ZodString>;
                    description: z.ZodOptional<z.ZodString>;
                    target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                    rank: z.ZodOptional<z.ZodNumber>;
                    position_x: z.ZodOptional<z.ZodNumber>;
                    position_y: z.ZodOptional<z.ZodNumber>;
                    manual_completed_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                    manual_completed_by_user_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                    version: z.ZodOptional<z.ZodNumber>;
                    task_total: z.ZodOptional<z.ZodNumber>;
                    task_done: z.ZodOptional<z.ZodNumber>;
                    progress_percentage: z.ZodOptional<z.ZodNumber>;
                    status: z.ZodOptional<z.ZodString>;
                    tasks: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                        id: z.ZodString;
                        space_id: z.ZodString;
                        task_number: z.ZodNumber;
                        task_key: z.ZodString;
                        title: z.ZodString;
                        notes: z.ZodString;
                        status: z.ZodString;
                        priority: z.ZodString;
                        rank: z.ZodNumber;
                        assignee_user_id: z.ZodOptional<z.ZodString>;
                        due_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                        due_timezone: z.ZodString;
                        source_refs: z.ZodJSONSchema;
                        created_by_user_id: z.ZodOptional<z.ZodString>;
                        created_by_agent_id: z.ZodOptional<z.ZodString>;
                        source_run_id: z.ZodOptional<z.ZodString>;
                        audience_kind: z.ZodString;
                        audience_conversation_id: z.ZodOptional<z.ZodString>;
                        audience_creator_user_id: z.ZodOptional<z.ZodString>;
                        version: z.ZodNumber;
                        completed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                        archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                        schedule: z.ZodOptional<z.ZodJSONSchema>;
                        calendar: z.ZodOptional<z.ZodJSONSchema>;
                        conflicted_fields: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
                        created_at: z.ZodString;
                        updated_at: z.ZodString;
                    }, z.core.$loose>>>>;
                    created_at: z.ZodOptional<z.ZodString>;
                    updated_at: z.ZodOptional<z.ZodString>;
                    id: z.ZodString;
                }, z.core.$loose>>>>;
                nodes: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                    space_id: z.ZodOptional<z.ZodString>;
                    roadmap_id: z.ZodOptional<z.ZodString>;
                    milestone_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                    definition_id: z.ZodOptional<z.ZodOptional<z.ZodString>>;
                    node_kind: z.ZodOptional<z.ZodString>;
                    title: z.ZodOptional<z.ZodString>;
                    description: z.ZodOptional<z.ZodString>;
                    target_date: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                    position_x: z.ZodOptional<z.ZodNumber>;
                    position_y: z.ZodOptional<z.ZodNumber>;
                    field_values: z.ZodOptional<z.ZodJSONSchema>;
                    version: z.ZodOptional<z.ZodNumber>;
                    archived_at: z.ZodOptional<z.ZodOptional<z.ZodNullable<z.ZodString>>>;
                    created_at: z.ZodOptional<z.ZodString>;
                    updated_at: z.ZodOptional<z.ZodString>;
                    id: z.ZodString;
                }, z.core.$loose>>>>;
                expected_version: z.ZodNumber;
            }, z.core.$strict>;
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            graph_version: z.ZodNumber;
        }, z.core.$loose>;
    };
};
