import { z } from "zod";
export declare const MISTY_APP_PROTOCOL_VERSION: 2;
export declare const AppRpcErrorSchema: z.ZodObject<{
    code: z.ZodString;
    message: z.ZodOptional<z.ZodString>;
}, z.core.$loose>;
export type AppRpcError = z.output<typeof AppRpcErrorSchema>;
export type MethodContract = Readonly<{
    verb: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
    path: string;
    params: z.ZodType;
    result: z.ZodType;
}>;
/** Only app capabilities belong here. Host credentials and administrative routes are excluded. */
export declare const mistyServerContracts: {
    readonly "spaces.get": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            security_domain_id: z.ZodString;
            owner_user_id: z.ZodString;
            name: z.ZodString;
            is_default: z.ZodBoolean;
            role: z.ZodString;
            member_count: z.ZodNumber;
            pending_count: z.ZodNumber;
            is_shared: z.ZodBoolean;
            permissions: z.ZodNullable<z.ZodRecord<z.ZodString, z.ZodBoolean>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "spaces.members.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/members";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            members: z.ZodNullable<z.ZodArray<z.ZodObject<{
                space_id: z.ZodString;
                user_id: z.ZodString;
                name: z.ZodString;
                email: z.ZodString;
                role: z.ZodString;
                joined_at: z.ZodString;
                read_message_seq: z.ZodNumber;
            }, z.core.$loose>>>;
            agents: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                agent_id: z.ZodString;
                owner_user_id: z.ZodString;
                can_control: z.ZodBoolean;
                name: z.ZodString;
                description: z.ZodString;
                icon: z.ZodString;
                avatar: z.ZodJSONSchema;
                instructions: z.ZodOptional<z.ZodString>;
                model_id: z.ZodOptional<z.ZodString>;
                reasoning_effort: z.ZodOptional<z.ZodString>;
                default_run_mode: z.ZodString;
                enabled: z.ZodBoolean;
                version: z.ZodNumber;
                created_at: z.ZodString;
                updated_at: z.ZodString;
                work_state: z.ZodString;
                attention_count: z.ZodNumber;
                last_activity_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                current_task_id: z.ZodOptional<z.ZodString>;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "notes.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/notes";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            notes: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                creator_user_id: z.ZodString;
                title: z.ZodString;
                markdown: z.ZodOptional<z.ZodString>;
                plain_text: z.ZodOptional<z.ZodString>;
                lifecycle_state: z.ZodString;
                collaboration_revision: z.ZodNumber;
                acl_version: z.ZodNumber;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
                role: z.ZodEnum<{
                    creator: "creator";
                    editor: "editor";
                    viewer: "viewer";
                }>;
                can_delete: z.ZodBoolean;
                backlink_count: z.ZodNumber;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "notes.get": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/notes/{noteID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            creator_user_id: z.ZodString;
            title: z.ZodString;
            markdown: z.ZodOptional<z.ZodString>;
            plain_text: z.ZodOptional<z.ZodString>;
            lifecycle_state: z.ZodString;
            collaboration_revision: z.ZodNumber;
            acl_version: z.ZodNumber;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
            can_delete: z.ZodBoolean;
            backlink_count: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "notes.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/notes";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                title: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            creator_user_id: z.ZodString;
            title: z.ZodString;
            markdown: z.ZodOptional<z.ZodString>;
            plain_text: z.ZodOptional<z.ZodString>;
            lifecycle_state: z.ZodString;
            collaboration_revision: z.ZodNumber;
            acl_version: z.ZodNumber;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
            can_delete: z.ZodBoolean;
            backlink_count: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "notes.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/notes/{noteID}/metadata";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                shared_tags: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodString>>>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            creator_user_id: z.ZodString;
            title: z.ZodString;
            markdown: z.ZodOptional<z.ZodString>;
            plain_text: z.ZodOptional<z.ZodString>;
            lifecycle_state: z.ZodString;
            collaboration_revision: z.ZodNumber;
            acl_version: z.ZodNumber;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
            can_delete: z.ZodBoolean;
            backlink_count: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "notes.archive": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/notes/{noteID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                archived: z.ZodBoolean;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "notes.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/notes/{noteID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "notes.backlinks": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/notes/{noteID}/backlinks";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            backlinks: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                title: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "notes.collaboration.ticket": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/notes/{noteID}/collaboration-ticket";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            ticket: z.ZodString;
            room: z.ZodString;
            url: z.ZodString;
            role: z.ZodString;
            expires_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "drawings.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/drawings";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            drawings: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                creator_user_id: z.ZodString;
                title: z.ZodString;
                lifecycle_state: z.ZodString;
                collaboration_revision: z.ZodNumber;
                acl_version: z.ZodNumber;
                created_at: z.ZodString;
                updated_at: z.ZodString;
                role: z.ZodEnum<{
                    creator: "creator";
                    editor: "editor";
                    viewer: "viewer";
                }>;
                can_delete: z.ZodBoolean;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "drawings.get": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            creator_user_id: z.ZodString;
            title: z.ZodString;
            lifecycle_state: z.ZodString;
            collaboration_revision: z.ZodNumber;
            acl_version: z.ZodNumber;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
            can_delete: z.ZodBoolean;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
        }, z.core.$loose>;
    };
    readonly "drawings.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/drawings";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                title: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            creator_user_id: z.ZodString;
            title: z.ZodString;
            lifecycle_state: z.ZodString;
            collaboration_revision: z.ZodNumber;
            acl_version: z.ZodNumber;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
            can_delete: z.ZodBoolean;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
        }, z.core.$loose>;
    };
    readonly "drawings.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                title: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            creator_user_id: z.ZodString;
            title: z.ZodString;
            lifecycle_state: z.ZodString;
            collaboration_revision: z.ZodNumber;
            acl_version: z.ZodNumber;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            role: z.ZodEnum<{
                creator: "creator";
                editor: "editor";
                viewer: "viewer";
            }>;
            can_delete: z.ZodBoolean;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
        }, z.core.$loose>;
    };
    readonly "drawings.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "drawings.collaboration.ticket": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}/collaboration-ticket";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            ticket: z.ZodString;
            room: z.ZodString;
            url: z.ZodString;
            role: z.ZodString;
            expires_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "tasks.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/tasks";
        readonly params: z.ZodObject<{
            query: z.ZodOptional<z.ZodObject<{
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
            }, z.core.$strict>>;
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
                assignee_agent_id: z.ZodOptional<z.ZodString>;
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
            next_cursor: z.ZodOptional<z.ZodString>;
            status_totals: z.ZodNullable<z.ZodRecord<z.ZodString, z.ZodNumber>>;
        }, z.core.$loose>;
    };
    readonly "tasks.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/tasks";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
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
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
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
            assignee_agent_id: z.ZodOptional<z.ZodString>;
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
    };
    readonly "tasks.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/tasks/{taskID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                taskID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
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
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
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
            assignee_agent_id: z.ZodOptional<z.ZodString>;
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
    };
    readonly "tasks.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/tasks/{taskID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                taskID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
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
            assignee_agent_id: z.ZodOptional<z.ZodString>;
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
    };
    readonly "roadmaps.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/roadmaps";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            roadmaps: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                name: z.ZodString;
                description: z.ZodString;
                graph_version: z.ZodNumber;
                created_by_user_id: z.ZodString;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "roadmaps.get": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            roadmap: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                name: z.ZodString;
                description: z.ZodString;
                graph_version: z.ZodNumber;
                created_by_user_id: z.ZodString;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            milestones: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
            }, z.core.$loose>>>;
            goals: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
                    assignee_agent_id: z.ZodOptional<z.ZodString>;
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
            }, z.core.$loose>>>;
            nodes: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
            }, z.core.$loose>>>;
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
            edges: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
            }, z.core.$loose>>>;
            goal_total: z.ZodNumber;
            goal_done: z.ZodNumber;
            milestone_total: z.ZodNumber;
            milestone_done: z.ZodNumber;
            progress_percentage: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/roadmaps";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                name: z.ZodString;
                description: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            roadmap: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                name: z.ZodString;
                description: z.ZodString;
                graph_version: z.ZodNumber;
                created_by_user_id: z.ZodString;
                audience_kind: z.ZodString;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$loose>;
            milestones: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
            }, z.core.$loose>>>;
            goals: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
                    assignee_agent_id: z.ZodOptional<z.ZodString>;
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
            }, z.core.$loose>>>;
            nodes: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
            }, z.core.$loose>>>;
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
            edges: z.ZodNullable<z.ZodArray<z.ZodObject<{
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
            }, z.core.$loose>>>;
            goal_total: z.ZodNumber;
            goal_done: z.ZodNumber;
            milestone_total: z.ZodNumber;
            milestone_done: z.ZodNumber;
            progress_percentage: z.ZodNumber;
        }, z.core.$loose>;
    };
    readonly "roadmaps.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/roadmaps/{roadmapID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                roadmapID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                name: z.ZodString;
                description: z.ZodOptional<z.ZodString>;
                expected_version: z.ZodNumber;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            name: z.ZodString;
            description: z.ZodString;
            graph_version: z.ZodNumber;
            created_by_user_id: z.ZodString;
            audience_kind: z.ZodString;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
            archived_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
        }, z.core.$loose>;
    };
    readonly "calendar.events.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/calendar/events";
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
            events: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                source_id: z.ZodString;
                provider: z.ZodString;
                external_event_id: z.ZodString;
                fingerprint: z.ZodString;
                title: z.ZodString;
                description: z.ZodString;
                location: z.ZodString;
                meeting_url: z.ZodString;
                organizer: z.ZodJSONSchema;
                starts_at: z.ZodString;
                ends_at: z.ZodString;
                all_day: z.ZodBoolean;
                timezone: z.ZodString;
                status: z.ZodString;
                provider_created_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                provider_updated_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                removed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
                created_at: z.ZodString;
                updated_at: z.ZodString;
                origin: z.ZodOptional<z.ZodString>;
                version: z.ZodOptional<z.ZodNumber>;
                audience_kind: z.ZodOptional<z.ZodString>;
                audience_conversation_id: z.ZodOptional<z.ZodString>;
                created_by_user_id: z.ZodOptional<z.ZodString>;
                created_by_agent_id: z.ZodOptional<z.ZodString>;
                source_run_id: z.ZodOptional<z.ZodString>;
            }, z.core.$loose>>>;
        }, z.core.$loose>;
    };
    readonly "calendar.events.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/calendar/events";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                title: z.ZodString;
                description: z.ZodOptional<z.ZodString>;
                location: z.ZodOptional<z.ZodString>;
                starts_at: z.ZodISODateTime;
                ends_at: z.ZodISODateTime;
                all_day: z.ZodOptional<z.ZodBoolean>;
                timezone: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            source_id: z.ZodString;
            provider: z.ZodString;
            external_event_id: z.ZodString;
            fingerprint: z.ZodString;
            title: z.ZodString;
            description: z.ZodString;
            location: z.ZodString;
            meeting_url: z.ZodString;
            organizer: z.ZodJSONSchema;
            starts_at: z.ZodString;
            ends_at: z.ZodString;
            all_day: z.ZodBoolean;
            timezone: z.ZodString;
            status: z.ZodString;
            provider_created_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            provider_updated_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            removed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            origin: z.ZodOptional<z.ZodString>;
            version: z.ZodOptional<z.ZodNumber>;
            audience_kind: z.ZodOptional<z.ZodString>;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
            created_by_user_id: z.ZodOptional<z.ZodString>;
            created_by_agent_id: z.ZodOptional<z.ZodString>;
            source_run_id: z.ZodOptional<z.ZodString>;
        }, z.core.$loose>;
    };
    readonly "calendar.events.update": {
        readonly verb: "PATCH";
        readonly path: "/spaces/{spaceID}/calendar/events/{eventID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                eventID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
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
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            space_id: z.ZodString;
            source_id: z.ZodString;
            provider: z.ZodString;
            external_event_id: z.ZodString;
            fingerprint: z.ZodString;
            title: z.ZodString;
            description: z.ZodString;
            location: z.ZodString;
            meeting_url: z.ZodString;
            organizer: z.ZodJSONSchema;
            starts_at: z.ZodString;
            ends_at: z.ZodString;
            all_day: z.ZodBoolean;
            timezone: z.ZodString;
            status: z.ZodString;
            provider_created_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            provider_updated_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            removed_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            created_at: z.ZodString;
            updated_at: z.ZodString;
            origin: z.ZodOptional<z.ZodString>;
            version: z.ZodOptional<z.ZodNumber>;
            audience_kind: z.ZodOptional<z.ZodString>;
            audience_conversation_id: z.ZodOptional<z.ZodString>;
            created_by_user_id: z.ZodOptional<z.ZodString>;
            created_by_agent_id: z.ZodOptional<z.ZodString>;
            source_run_id: z.ZodOptional<z.ZodString>;
        }, z.core.$loose>;
    };
    readonly "calendar.events.delete": {
        readonly verb: "DELETE";
        readonly path: "/spaces/{spaceID}/calendar/events/{eventID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                eventID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                version: z.ZodUnion<readonly [z.ZodNumber, z.ZodString]>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "mail.accounts.list": {
        readonly verb: "GET";
        readonly path: "/mail/accounts";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            accounts: z.ZodArray<z.ZodObject<{
                connection_id: z.ZodString;
                provider: z.ZodString;
                account_id: z.ZodString;
                email: z.ZodString;
                display_name: z.ZodString;
                total: z.ZodNumber;
                unread: z.ZodNumber;
                status: z.ZodOptional<z.ZodString>;
                error_code: z.ZodOptional<z.ZodString>;
            }, z.core.$strip>>;
        }, z.core.$strip>;
    };
    readonly "mail.folders.list": {
        readonly verb: "GET";
        readonly path: "/mail/folders";
        readonly params: z.ZodObject<{
            query: z.ZodObject<{
                connection_id: z.ZodString;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            folders: z.ZodArray<z.ZodObject<{
                provider: z.ZodString;
                provider_id: z.ZodString;
                account_id: z.ZodString;
                name: z.ZodString;
                kind: z.ZodString;
                system: z.ZodBoolean;
                total: z.ZodNumber;
                unread: z.ZodNumber;
                text_color: z.ZodOptional<z.ZodString>;
                background: z.ZodOptional<z.ZodString>;
            }, z.core.$strip>>;
        }, z.core.$strip>;
    };
    readonly "mail.threads.list": {
        readonly verb: "GET";
        readonly path: "/mail/threads";
        readonly params: z.ZodObject<{
            query: z.ZodObject<{
                connection_id: z.ZodString;
                folder_id: z.ZodOptional<z.ZodString>;
                query: z.ZodOptional<z.ZodString>;
                page_token: z.ZodOptional<z.ZodString>;
                page_size: z.ZodOptional<z.ZodNumber>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            threads: z.ZodArray<z.ZodObject<{
                provider: z.ZodString;
                provider_id: z.ZodString;
                account_id: z.ZodString;
                subject: z.ZodString;
                snippet: z.ZodString;
                participants: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>>;
                labels: z.ZodArray<z.ZodString>;
                last_message_at: z.ZodString;
                unread: z.ZodBoolean;
                starred: z.ZodBoolean;
                messages: z.ZodArray<z.ZodObject<{
                    provider: z.ZodString;
                    provider_id: z.ZodString;
                    account_id: z.ZodString;
                    thread_id: z.ZodString;
                    rfc822_id: z.ZodOptional<z.ZodString>;
                    subject: z.ZodString;
                    from: z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>;
                    to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    cc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    bcc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    reply_to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    sent_at: z.ZodString;
                    snippet: z.ZodString;
                    body: z.ZodObject<{
                        text: z.ZodString;
                        html: z.ZodOptional<z.ZodString>;
                        had_html: z.ZodBoolean;
                        truncated: z.ZodBoolean;
                    }, z.core.$strip>;
                    labels: z.ZodArray<z.ZodString>;
                    unread: z.ZodBoolean;
                    starred: z.ZodBoolean;
                    draft: z.ZodBoolean;
                    attachments: z.ZodArray<z.ZodObject<{
                        provider: z.ZodString;
                        provider_id: z.ZodString;
                        account_id: z.ZodString;
                        message_id: z.ZodString;
                        filename: z.ZodString;
                        content_type: z.ZodString;
                        size: z.ZodNumber;
                        inline: z.ZodBoolean;
                        content_id: z.ZodOptional<z.ZodString>;
                    }, z.core.$strip>>;
                }, z.core.$strip>>;
            }, z.core.$strip>>;
            next_page_token: z.ZodOptional<z.ZodString>;
            estimated_total: z.ZodOptional<z.ZodNumber>;
        }, z.core.$strip>;
    };
    readonly "mail.threads.get": {
        readonly verb: "GET";
        readonly path: "/mail/threads/{threadID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                threadID: z.ZodString;
            }, z.core.$strict>;
            query: z.ZodObject<{
                connection_id: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            thread: z.ZodObject<{
                provider: z.ZodString;
                provider_id: z.ZodString;
                account_id: z.ZodString;
                subject: z.ZodString;
                snippet: z.ZodString;
                participants: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>>;
                labels: z.ZodArray<z.ZodString>;
                last_message_at: z.ZodString;
                unread: z.ZodBoolean;
                starred: z.ZodBoolean;
                messages: z.ZodArray<z.ZodObject<{
                    provider: z.ZodString;
                    provider_id: z.ZodString;
                    account_id: z.ZodString;
                    thread_id: z.ZodString;
                    rfc822_id: z.ZodOptional<z.ZodString>;
                    subject: z.ZodString;
                    from: z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>;
                    to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    cc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    bcc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    reply_to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    sent_at: z.ZodString;
                    snippet: z.ZodString;
                    body: z.ZodObject<{
                        text: z.ZodString;
                        html: z.ZodOptional<z.ZodString>;
                        had_html: z.ZodBoolean;
                        truncated: z.ZodBoolean;
                    }, z.core.$strip>;
                    labels: z.ZodArray<z.ZodString>;
                    unread: z.ZodBoolean;
                    starred: z.ZodBoolean;
                    draft: z.ZodBoolean;
                    attachments: z.ZodArray<z.ZodObject<{
                        provider: z.ZodString;
                        provider_id: z.ZodString;
                        account_id: z.ZodString;
                        message_id: z.ZodString;
                        filename: z.ZodString;
                        content_type: z.ZodString;
                        size: z.ZodNumber;
                        inline: z.ZodBoolean;
                        content_id: z.ZodOptional<z.ZodString>;
                    }, z.core.$strip>>;
                }, z.core.$strip>>;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "mail.threads.action": {
        readonly verb: "POST";
        readonly path: "/mail/threads/{threadID}/actions";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                threadID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                connection_id: z.ZodString;
                read: z.ZodOptional<z.ZodBoolean>;
                archived: z.ZodOptional<z.ZodBoolean>;
                starred: z.ZodOptional<z.ZodBoolean>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            thread_id: z.ZodString;
            added_labels: z.ZodArray<z.ZodString>;
            removed_labels: z.ZodArray<z.ZodString>;
        }, z.core.$strip>;
    };
    readonly "mail.drafts.create": {
        readonly verb: "POST";
        readonly path: "/mail/drafts";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                connection_id: z.ZodString;
                thread_id: z.ZodOptional<z.ZodString>;
                to: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>;
                cc: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>>;
                bcc: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>>;
                reply_to: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>>;
                subject: z.ZodString;
                text: z.ZodString;
                attachments: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    filename: z.ZodString;
                    content_type: z.ZodString;
                    data: z.ZodString;
                    inline: z.ZodBoolean;
                    content_id: z.ZodOptional<z.ZodString>;
                }, z.core.$strict>>>;
            }, z.core.$strict>;
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            draft: z.ZodObject<{
                provider: z.ZodString;
                provider_id: z.ZodString;
                account_id: z.ZodString;
                thread_id: z.ZodOptional<z.ZodString>;
                message: z.ZodObject<{
                    provider: z.ZodString;
                    provider_id: z.ZodString;
                    account_id: z.ZodString;
                    thread_id: z.ZodString;
                    rfc822_id: z.ZodOptional<z.ZodString>;
                    subject: z.ZodString;
                    from: z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>;
                    to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    cc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    bcc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    reply_to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    sent_at: z.ZodString;
                    snippet: z.ZodString;
                    body: z.ZodObject<{
                        text: z.ZodString;
                        html: z.ZodOptional<z.ZodString>;
                        had_html: z.ZodBoolean;
                        truncated: z.ZodBoolean;
                    }, z.core.$strip>;
                    labels: z.ZodArray<z.ZodString>;
                    unread: z.ZodBoolean;
                    starred: z.ZodBoolean;
                    draft: z.ZodBoolean;
                    attachments: z.ZodArray<z.ZodObject<{
                        provider: z.ZodString;
                        provider_id: z.ZodString;
                        account_id: z.ZodString;
                        message_id: z.ZodString;
                        filename: z.ZodString;
                        content_type: z.ZodString;
                        size: z.ZodNumber;
                        inline: z.ZodBoolean;
                        content_id: z.ZodOptional<z.ZodString>;
                    }, z.core.$strip>>;
                }, z.core.$strip>;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "mail.drafts.update": {
        readonly verb: "PUT";
        readonly path: "/mail/drafts/{draftID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                draftID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                connection_id: z.ZodString;
                thread_id: z.ZodOptional<z.ZodString>;
                to: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>;
                cc: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>>;
                bcc: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>>;
                reply_to: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strict>>>;
                subject: z.ZodString;
                text: z.ZodString;
                attachments: z.ZodOptional<z.ZodArray<z.ZodObject<{
                    filename: z.ZodString;
                    content_type: z.ZodString;
                    data: z.ZodString;
                    inline: z.ZodBoolean;
                    content_id: z.ZodOptional<z.ZodString>;
                }, z.core.$strict>>>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            draft: z.ZodObject<{
                provider: z.ZodString;
                provider_id: z.ZodString;
                account_id: z.ZodString;
                thread_id: z.ZodOptional<z.ZodString>;
                message: z.ZodObject<{
                    provider: z.ZodString;
                    provider_id: z.ZodString;
                    account_id: z.ZodString;
                    thread_id: z.ZodString;
                    rfc822_id: z.ZodOptional<z.ZodString>;
                    subject: z.ZodString;
                    from: z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>;
                    to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    cc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    bcc: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    reply_to: z.ZodArray<z.ZodObject<{
                        name: z.ZodOptional<z.ZodString>;
                        email: z.ZodString;
                    }, z.core.$strip>>;
                    sent_at: z.ZodString;
                    snippet: z.ZodString;
                    body: z.ZodObject<{
                        text: z.ZodString;
                        html: z.ZodOptional<z.ZodString>;
                        had_html: z.ZodBoolean;
                        truncated: z.ZodBoolean;
                    }, z.core.$strip>;
                    labels: z.ZodArray<z.ZodString>;
                    unread: z.ZodBoolean;
                    starred: z.ZodBoolean;
                    draft: z.ZodBoolean;
                    attachments: z.ZodArray<z.ZodObject<{
                        provider: z.ZodString;
                        provider_id: z.ZodString;
                        account_id: z.ZodString;
                        message_id: z.ZodString;
                        filename: z.ZodString;
                        content_type: z.ZodString;
                        size: z.ZodNumber;
                        inline: z.ZodBoolean;
                        content_id: z.ZodOptional<z.ZodString>;
                    }, z.core.$strip>>;
                }, z.core.$strip>;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "mail.drafts.send": {
        readonly verb: "POST";
        readonly path: "/mail/drafts/{draftID}/send";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                draftID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                connection_id: z.ZodString;
                authoring_source: z.ZodEnum<{
                    user: "user";
                    ai: "ai";
                }>;
                confirmed: z.ZodLiteral<true>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            message: z.ZodObject<{
                provider: z.ZodString;
                provider_id: z.ZodString;
                account_id: z.ZodString;
                thread_id: z.ZodString;
                rfc822_id: z.ZodOptional<z.ZodString>;
                subject: z.ZodString;
                from: z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>;
                to: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>>;
                cc: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>>;
                bcc: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>>;
                reply_to: z.ZodArray<z.ZodObject<{
                    name: z.ZodOptional<z.ZodString>;
                    email: z.ZodString;
                }, z.core.$strip>>;
                sent_at: z.ZodString;
                snippet: z.ZodString;
                body: z.ZodObject<{
                    text: z.ZodString;
                    html: z.ZodOptional<z.ZodString>;
                    had_html: z.ZodBoolean;
                    truncated: z.ZodBoolean;
                }, z.core.$strip>;
                labels: z.ZodArray<z.ZodString>;
                unread: z.ZodBoolean;
                starred: z.ZodBoolean;
                draft: z.ZodBoolean;
                attachments: z.ZodArray<z.ZodObject<{
                    provider: z.ZodString;
                    provider_id: z.ZodString;
                    account_id: z.ZodString;
                    message_id: z.ZodString;
                    filename: z.ZodString;
                    content_type: z.ZodString;
                    size: z.ZodNumber;
                    inline: z.ZodBoolean;
                    content_id: z.ZodOptional<z.ZodString>;
                }, z.core.$strip>>;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "connections.list": {
        readonly verb: "GET";
        readonly path: "/connections";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            connections: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                provider: z.ZodString;
                account_id: z.ZodString;
                account_display: z.ZodString;
                capabilities: z.ZodNullable<z.ZodArray<z.ZodString>>;
                granted_scopes: z.ZodNullable<z.ZodArray<z.ZodString>>;
                status: z.ZodEnum<{
                    active: "active";
                    needs_attention: "needs_attention";
                    revoked: "revoked";
                }>;
                last_error_code: z.ZodOptional<z.ZodString>;
                expires_at: z.ZodOptional<z.ZodNullable<z.ZodString>>;
            }, z.core.$strip>>>;
            providers: z.ZodOptional<z.ZodRecord<z.ZodString, z.ZodBoolean>>;
        }, z.core.$strip>;
    };
    readonly "connections.remove": {
        readonly verb: "DELETE";
        readonly path: "/connections/{connectionID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                connectionID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "connections.authorize": {
        readonly verb: "POST";
        readonly path: "/connections/{provider}/authorize";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                provider: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                capabilities: z.ZodArray<z.ZodString>;
                return_to: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            provider: z.ZodString;
            authorization_url: z.ZodURL;
            state_expires_at: z.ZodOptional<z.ZodString>;
        }, z.core.$strip>;
    };
    readonly "integrations.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/integrations";
        readonly params: z.ZodObject<{
            path: z.ZodOptional<z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            integrations: z.ZodNullable<z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                provider: z.ZodString;
                display_name: z.ZodString;
                granted_permissions: z.ZodNullable<z.ZodArray<z.ZodString>>;
                status: z.ZodString;
                connected_by_user_id: z.ZodString;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$strip>>>;
            providers: z.ZodOptional<z.ZodNullable<z.ZodArray<z.ZodObject<{
                provider: z.ZodString;
                configured: z.ZodBoolean;
            }, z.core.$strip>>>>;
        }, z.core.$strip>;
    };
    readonly "integrations.bind": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/integrations/{provider}/bind";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                provider: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                connection_id: z.ZodString;
                capability: z.ZodEnum<{
                    calendar_read: "calendar_read";
                    calendar_write: "calendar_write";
                }>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            integration: z.ZodObject<{
                id: z.ZodString;
                space_id: z.ZodString;
                provider: z.ZodString;
                display_name: z.ZodString;
                granted_permissions: z.ZodNullable<z.ZodArray<z.ZodString>>;
                status: z.ZodString;
                connected_by_user_id: z.ZodString;
                created_at: z.ZodString;
                updated_at: z.ZodString;
            }, z.core.$strip>;
            connection_id: z.ZodString;
            capability: z.ZodString;
        }, z.core.$strip>;
    };
    readonly "notes.assets.reserve": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/notes/{noteID}/assets/uploads";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                filename: z.ZodString;
                mime_type: z.ZodEnum<{
                    "image/png": "image/png";
                    "image/jpeg": "image/jpeg";
                    "image/webp": "image/webp";
                    "image/gif": "image/gif";
                    "image/avif": "image/avif";
                    "image/bmp": "image/bmp";
                    "image/x-icon": "image/x-icon";
                    "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
                }>;
                byte_size: z.ZodNumber;
                sha256: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            upload: z.ZodObject<{
                id: z.ZodString;
            }, z.core.$strip>;
            transfer: z.ZodObject<{
                url: z.ZodURL;
                method: z.ZodLiteral<"PUT">;
                headers: z.ZodRecord<z.ZodString, z.ZodString>;
                expires_at: z.ZodISODateTime;
            }, z.core.$strip>;
            finalize: z.ZodObject<{
                headers: z.ZodRecord<z.ZodString, z.ZodString>;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "notes.assets.finalize": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/notes/{noteID}/assets/uploads/{uploadID}/finalize";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
                uploadID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            note_asset: z.ZodObject<{
                id: z.ZodString;
                mime_type: z.ZodEnum<{
                    "image/png": "image/png";
                    "image/jpeg": "image/jpeg";
                    "image/webp": "image/webp";
                    "image/gif": "image/gif";
                    "image/avif": "image/avif";
                    "image/bmp": "image/bmp";
                    "image/x-icon": "image/x-icon";
                    "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
                }>;
                byte_size: z.ZodNumber;
                sha256: z.ZodString;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "notes.assets.download": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/notes/{noteID}/assets/{assetID}/download";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                noteID: z.ZodString;
                assetID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            url: z.ZodURL;
            expires_at: z.ZodISODateTime;
            filename: z.ZodString;
            mime_type: z.ZodEnum<{
                "image/png": "image/png";
                "image/jpeg": "image/jpeg";
                "image/webp": "image/webp";
                "image/gif": "image/gif";
                "image/avif": "image/avif";
                "image/bmp": "image/bmp";
                "image/x-icon": "image/x-icon";
                "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
            }>;
            byte_size: z.ZodNumber;
            sha256: z.ZodString;
        }, z.core.$strip>;
    };
    readonly "drawings.assets.reserve": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}/assets/uploads";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                filename: z.ZodString;
                mime_type: z.ZodEnum<{
                    "image/png": "image/png";
                    "image/jpeg": "image/jpeg";
                    "image/webp": "image/webp";
                    "image/gif": "image/gif";
                    "image/avif": "image/avif";
                    "image/bmp": "image/bmp";
                    "image/x-icon": "image/x-icon";
                    "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
                }>;
                byte_size: z.ZodNumber;
                sha256: z.ZodString;
                file_id: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            upload: z.ZodObject<{
                id: z.ZodString;
            }, z.core.$strip>;
            transfer: z.ZodObject<{
                url: z.ZodURL;
                method: z.ZodLiteral<"PUT">;
                headers: z.ZodRecord<z.ZodString, z.ZodString>;
                expires_at: z.ZodISODateTime;
            }, z.core.$strip>;
            finalize: z.ZodObject<{
                headers: z.ZodRecord<z.ZodString, z.ZodString>;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "drawings.assets.finalize": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}/assets/uploads/{uploadID}/finalize";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
                uploadID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            drawing_asset: z.ZodObject<{
                id: z.ZodString;
                mime_type: z.ZodEnum<{
                    "image/png": "image/png";
                    "image/jpeg": "image/jpeg";
                    "image/webp": "image/webp";
                    "image/gif": "image/gif";
                    "image/avif": "image/avif";
                    "image/bmp": "image/bmp";
                    "image/x-icon": "image/x-icon";
                    "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
                }>;
                byte_size: z.ZodNumber;
                sha256: z.ZodString;
                excalidraw_file_id: z.ZodString;
            }, z.core.$strip>;
        }, z.core.$strip>;
    };
    readonly "drawings.assets.download": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/drawings/{drawingID}/assets/{assetID}/download";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodOptional<z.ZodString>;
                drawingID: z.ZodString;
                assetID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            url: z.ZodURL;
            expires_at: z.ZodISODateTime;
            filename: z.ZodString;
            mime_type: z.ZodEnum<{
                "image/png": "image/png";
                "image/jpeg": "image/jpeg";
                "image/webp": "image/webp";
                "image/gif": "image/gif";
                "image/avif": "image/avif";
                "image/bmp": "image/bmp";
                "image/x-icon": "image/x-icon";
                "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
            }>;
            byte_size: z.ZodNumber;
            sha256: z.ZodString;
        }, z.core.$strip>;
    };
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
                assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                    assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                    assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                    assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                    assignee_agent_id: z.ZodOptional<z.ZodString>;
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
                        assignee_agent_id: z.ZodOptional<z.ZodString>;
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
export type MistyServerMethod = keyof typeof mistyServerContracts;
export type MistyMethodParams<M extends MistyServerMethod> = z.input<(typeof mistyServerContracts)[M]["params"]>;
export type MistyMethodResult<M extends MistyServerMethod> = z.output<(typeof mistyServerContracts)[M]["result"]>;
export declare const mistyServerMethods: { readonly [M in MistyServerMethod]: Pick<(typeof mistyServerContracts)[M], "verb" | "path">; };
export declare class MistyContractError extends Error {
    readonly code: "unsupported_protocol" | "unsupported_method" | "invalid_request" | "invalid_params" | "invalid_response" | "space_mismatch";
    constructor(code: "unsupported_protocol" | "unsupported_method" | "invalid_request" | "invalid_params" | "invalid_response" | "space_mismatch", message: string);
}
export declare function isMistyServerMethod(method: string): method is MistyServerMethod;
export declare function parseMethodParams<M extends MistyServerMethod>(method: M, value: unknown): MistyMethodParams<M>;
export declare function parseMethodResult<M extends MistyServerMethod>(method: M, value: unknown): MistyMethodResult<M>;
export declare const AppRpcEnvelopeSchema: z.ZodObject<{
    protocol: z.ZodNumber;
    method: z.ZodString;
    params: z.ZodOptional<z.ZodUnknown>;
}, z.core.$strict>;
export declare function parseAppRpcRequest(value: unknown, boundSpaceId: string): {
    protocol: 2;
    method: "spaces.get" | "spaces.members.list" | "notes.list" | "notes.get" | "notes.create" | "notes.update" | "notes.archive" | "notes.delete" | "notes.backlinks" | "notes.collaboration.ticket" | "drawings.list" | "drawings.get" | "drawings.create" | "drawings.update" | "drawings.delete" | "drawings.collaboration.ticket" | "tasks.list" | "tasks.create" | "tasks.update" | "tasks.delete" | "roadmaps.list" | "roadmaps.get" | "roadmaps.create" | "roadmaps.update" | "calendar.events.list" | "calendar.events.create" | "calendar.events.update" | "calendar.events.delete" | "mail.accounts.list" | "mail.folders.list" | "mail.threads.list" | "mail.threads.get" | "mail.threads.action" | "mail.drafts.create" | "mail.drafts.update" | "mail.drafts.send" | "connections.list" | "connections.remove" | "connections.authorize" | "integrations.list" | "integrations.bind" | "notes.assets.reserve" | "notes.assets.finalize" | "notes.assets.download" | "drawings.assets.reserve" | "drawings.assets.finalize" | "drawings.assets.download" | "tasks.activity.list" | "tasks.move" | "agenda.list" | "calendar.sources.list" | "calendar.sources.create" | "calendar.sources.delete" | "calendar.google.calendars" | "calendar.sync" | "roadmaps.delete" | "roadmaps.nodeDefinitions.list" | "roadmaps.nodeDefinitions.create" | "roadmaps.nodeDefinitions.update" | "roadmaps.nodeDefinitions.delete" | "roadmaps.milestones.create" | "roadmaps.milestones.update" | "roadmaps.milestones.delete" | "roadmaps.goals.create" | "roadmaps.goals.update" | "roadmaps.goals.delete" | "roadmaps.goals.setTasks" | "roadmaps.nodes.create" | "roadmaps.nodes.update" | "roadmaps.nodes.delete" | "roadmaps.edges.create" | "roadmaps.edges.update" | "roadmaps.edges.delete" | "roadmaps.layout.update";
    params: {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            title?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            shared_tags?: string[] | null | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            archived: boolean;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            title?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            title?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query?: {
            status?: "todo" | "in_progress" | "done" | "canceled" | undefined;
            priority?: "high" | "medium" | "low" | undefined;
            assignee_user_id?: string | undefined;
            assignee_agent_id?: string | undefined;
            q?: string | undefined;
            due_from?: string | undefined;
            due_to?: string | undefined;
            sort?: "rank" | "due" | "updated" | undefined;
            cursor?: string | undefined;
            limit?: string | number | undefined;
            include_archived?: boolean | "true" | "false" | undefined;
        } | undefined;
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            title: string;
            notes?: string | undefined;
            status?: "" | "todo" | "in_progress" | "done" | "canceled" | undefined;
            priority?: "" | "high" | "medium" | "low" | undefined;
            assignee_user_id?: string | undefined;
            assignee_agent_id?: string | undefined;
            due_at?: string | null | undefined;
            due_timezone?: string | undefined;
            source_refs?: {
                [x: string]: unknown;
                kind: string;
                resource_id: string;
            }[] | null | undefined;
            agent_run?: {
                [x: string]: unknown;
                mode?: string | undefined;
                context_references?: {
                    [x: string]: unknown;
                    device_id: string;
                    kind: string;
                    opaque_ref: string;
                    capabilities: z.core.util.JSONType;
                    display_name?: string | undefined;
                    metadata?: z.core.util.JSONType | undefined;
                }[] | null | undefined;
            } | null | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            task_number?: number | undefined;
            task_key?: string | undefined;
            rank?: number | undefined;
            created_by_user_id?: string | undefined;
            created_by_agent_id?: string | undefined;
            source_run_id?: string | undefined;
            audience_kind?: string | undefined;
            audience_conversation_id?: string | undefined;
            audience_creator_user_id?: string | undefined;
            completed_at?: string | null | undefined;
            archived_at?: string | null | undefined;
            schedule?: z.core.util.JSONType | undefined;
            calendar?: z.core.util.JSONType | undefined;
            conflicted_fields?: string[] | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
            notes?: string | undefined;
            status?: "" | "todo" | "in_progress" | "done" | "canceled" | undefined;
            priority?: "" | "high" | "medium" | "low" | undefined;
            assignee_user_id?: string | undefined;
            assignee_agent_id?: string | undefined;
            due_at?: string | null | undefined;
            due_timezone?: string | undefined;
            source_refs?: {
                [x: string]: unknown;
                kind: string;
                resource_id: string;
            }[] | null | undefined;
            agent_run?: {
                [x: string]: unknown;
                mode?: string | undefined;
                context_references?: {
                    [x: string]: unknown;
                    device_id: string;
                    kind: string;
                    opaque_ref: string;
                    capabilities: z.core.util.JSONType;
                    display_name?: string | undefined;
                    metadata?: z.core.util.JSONType | undefined;
                }[] | null | undefined;
            } | null | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            name: string;
            description?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            name: string;
            expected_version: number;
            description?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            from: string;
            to: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            title: string;
            starts_at: string;
            ends_at: string;
            description?: string | undefined;
            location?: string | undefined;
            all_day?: boolean | undefined;
            timezone?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            version: number;
            title: string;
            starts_at: string;
            ends_at: string;
            id?: string | undefined;
            space_id?: string | undefined;
            source_id?: string | undefined;
            provider?: string | undefined;
            external_event_id?: string | undefined;
            fingerprint?: string | undefined;
            meeting_url?: string | undefined;
            organizer?: z.core.util.JSONType | undefined;
            status?: string | undefined;
            provider_created_at?: string | null | undefined;
            provider_updated_at?: string | null | undefined;
            removed_at?: string | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
            origin?: string | undefined;
            audience_kind?: string | undefined;
            audience_conversation_id?: string | undefined;
            created_by_user_id?: string | undefined;
            created_by_agent_id?: string | undefined;
            source_run_id?: string | undefined;
            description?: string | undefined;
            location?: string | undefined;
            all_day?: boolean | undefined;
            timezone?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            connection_id: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            connection_id: string;
            folder_id?: string | undefined;
            query?: string | undefined;
            page_token?: string | undefined;
            page_size?: number | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            connection_id: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            connection_id: string;
            read?: boolean | undefined;
            archived?: boolean | undefined;
            starred?: boolean | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            connection_id: string;
            to: {
                email: string;
                name?: string | undefined;
            }[];
            subject: string;
            text: string;
            thread_id?: string | undefined;
            cc?: {
                email: string;
                name?: string | undefined;
            }[] | undefined;
            bcc?: {
                email: string;
                name?: string | undefined;
            }[] | undefined;
            reply_to?: {
                email: string;
                name?: string | undefined;
            }[] | undefined;
            attachments?: {
                filename: string;
                content_type: string;
                data: string;
                inline: boolean;
                content_id?: string | undefined;
            }[] | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            connection_id: string;
            to: {
                email: string;
                name?: string | undefined;
            }[];
            subject: string;
            text: string;
            thread_id?: string | undefined;
            cc?: {
                email: string;
                name?: string | undefined;
            }[] | undefined;
            bcc?: {
                email: string;
                name?: string | undefined;
            }[] | undefined;
            reply_to?: {
                email: string;
                name?: string | undefined;
            }[] | undefined;
            attachments?: {
                filename: string;
                content_type: string;
                data: string;
                inline: boolean;
                content_id?: string | undefined;
            }[] | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            connection_id: string;
            authoring_source: "user" | "ai";
            confirmed: true;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            capabilities: string[];
            return_to: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            connection_id: string;
            capability: "calendar_read" | "calendar_write";
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            filename: string;
            mime_type: "image/png" | "image/jpeg" | "image/webp" | "image/gif" | "image/avif" | "image/bmp" | "image/x-icon" | "image/vnd.microsoft.icon";
            byte_size: number;
            sha256: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            filename: string;
            mime_type: "image/png" | "image/jpeg" | "image/webp" | "image/gif" | "image/avif" | "image/bmp" | "image/x-icon" | "image/vnd.microsoft.icon";
            byte_size: number;
            sha256: string;
            file_id: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            version: number;
            status: "todo" | "in_progress" | "done" | "canceled";
            before_task_id?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            from: string;
            to: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            integration_id: string;
            external_calendar_id: string;
            display_name?: string | undefined;
            timezone?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            integration_id: string;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            source_id?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            expected_version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            name: string;
            id?: string | undefined;
            space_id?: string | undefined;
            description?: string | undefined;
            icon?: string | undefined;
            color?: string | undefined;
            agenda_visible?: boolean | undefined;
            field_schema?: z.core.util.JSONType | undefined;
            version?: number | undefined;
            created_by_user_id?: string | undefined;
            archived_at?: string | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            name: string;
            expected_version: number;
            id?: string | undefined;
            space_id?: string | undefined;
            description?: string | undefined;
            icon?: string | undefined;
            color?: string | undefined;
            agenda_visible?: boolean | undefined;
            field_schema?: z.core.util.JSONType | undefined;
            version?: number | undefined;
            created_by_user_id?: string | undefined;
            archived_at?: string | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            expected_version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            description?: string | undefined;
            target_date?: string | null | undefined;
            rank?: number | undefined;
            position_x?: number | undefined;
            position_y?: number | undefined;
            width?: number | undefined;
            height?: number | undefined;
            version?: number | undefined;
            goal_total?: number | undefined;
            goal_done?: number | undefined;
            status?: string | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            description?: string | undefined;
            target_date?: string | null | undefined;
            rank?: number | undefined;
            position_x?: number | undefined;
            position_y?: number | undefined;
            width?: number | undefined;
            height?: number | undefined;
            version?: number | undefined;
            goal_total?: number | undefined;
            goal_done?: number | undefined;
            status?: string | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            expected_version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            milestone_id?: string | undefined;
            description?: string | undefined;
            target_date?: string | null | undefined;
            rank?: number | undefined;
            position_x?: number | undefined;
            position_y?: number | undefined;
            manual_completed_at?: string | null | undefined;
            manual_completed_by_user_id?: string | undefined;
            version?: number | undefined;
            task_total?: number | undefined;
            task_done?: number | undefined;
            progress_percentage?: number | undefined;
            status?: string | undefined;
            tasks?: {
                [x: string]: unknown;
                id: string;
                space_id: string;
                task_number: number;
                task_key: string;
                title: string;
                notes: string;
                status: string;
                priority: string;
                rank: number;
                due_timezone: string;
                source_refs: z.core.util.JSONType;
                audience_kind: string;
                version: number;
                created_at: string;
                updated_at: string;
                assignee_user_id?: string | undefined;
                assignee_agent_id?: string | undefined;
                agent_run?: {
                    [x: string]: unknown;
                    mode?: string | undefined;
                    context_references?: {
                        [x: string]: unknown;
                        device_id: string;
                        kind: string;
                        opaque_ref: string;
                        capabilities: z.core.util.JSONType;
                        display_name?: string | undefined;
                        metadata?: z.core.util.JSONType | undefined;
                    }[] | null | undefined;
                } | null | undefined;
                due_at?: string | null | undefined;
                created_by_user_id?: string | undefined;
                created_by_agent_id?: string | undefined;
                source_run_id?: string | undefined;
                audience_conversation_id?: string | undefined;
                audience_creator_user_id?: string | undefined;
                completed_at?: string | null | undefined;
                archived_at?: string | null | undefined;
                schedule?: z.core.util.JSONType | undefined;
                calendar?: z.core.util.JSONType | undefined;
                conflicted_fields?: string[] | null | undefined;
            }[] | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            milestone_id?: string | undefined;
            description?: string | undefined;
            target_date?: string | null | undefined;
            rank?: number | undefined;
            position_x?: number | undefined;
            position_y?: number | undefined;
            manual_completed_at?: string | null | undefined;
            manual_completed_by_user_id?: string | undefined;
            version?: number | undefined;
            task_total?: number | undefined;
            task_done?: number | undefined;
            progress_percentage?: number | undefined;
            status?: string | undefined;
            tasks?: {
                [x: string]: unknown;
                id: string;
                space_id: string;
                task_number: number;
                task_key: string;
                title: string;
                notes: string;
                status: string;
                priority: string;
                rank: number;
                due_timezone: string;
                source_refs: z.core.util.JSONType;
                audience_kind: string;
                version: number;
                created_at: string;
                updated_at: string;
                assignee_user_id?: string | undefined;
                assignee_agent_id?: string | undefined;
                agent_run?: {
                    [x: string]: unknown;
                    mode?: string | undefined;
                    context_references?: {
                        [x: string]: unknown;
                        device_id: string;
                        kind: string;
                        opaque_ref: string;
                        capabilities: z.core.util.JSONType;
                        display_name?: string | undefined;
                        metadata?: z.core.util.JSONType | undefined;
                    }[] | null | undefined;
                } | null | undefined;
                due_at?: string | null | undefined;
                created_by_user_id?: string | undefined;
                created_by_agent_id?: string | undefined;
                source_run_id?: string | undefined;
                audience_conversation_id?: string | undefined;
                audience_creator_user_id?: string | undefined;
                completed_at?: string | null | undefined;
                archived_at?: string | null | undefined;
                schedule?: z.core.util.JSONType | undefined;
                calendar?: z.core.util.JSONType | undefined;
                conflicted_fields?: string[] | null | undefined;
            }[] | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
            complete_manually?: boolean | null | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            expected_version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            expected_version: number;
            task_ids: string[] | null;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            milestone_id?: string | undefined;
            definition_id?: string | undefined;
            node_kind?: string | undefined;
            description?: string | undefined;
            target_date?: string | null | undefined;
            position_x?: number | undefined;
            position_y?: number | undefined;
            field_values?: z.core.util.JSONType | undefined;
            version?: number | undefined;
            archived_at?: string | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            title: string;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            milestone_id?: string | undefined;
            definition_id?: string | undefined;
            node_kind?: string | undefined;
            description?: string | undefined;
            target_date?: string | null | undefined;
            position_x?: number | undefined;
            position_y?: number | undefined;
            field_values?: z.core.util.JSONType | undefined;
            version?: number | undefined;
            archived_at?: string | null | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            expected_version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            source?: {
                [x: string]: unknown;
                kind: string;
                id: string;
            } | undefined;
            target?: {
                [x: string]: unknown;
                kind: string;
                id: string;
            } | undefined;
            source_goal_id?: string | undefined;
            target_goal_id?: string | undefined;
            edge_type?: string | undefined;
            label?: string | undefined;
            version?: number | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            [x: string]: unknown;
            expected_version: number;
            id?: string | undefined;
            space_id?: string | undefined;
            roadmap_id?: string | undefined;
            source?: {
                [x: string]: unknown;
                kind: string;
                id: string;
            } | undefined;
            target?: {
                [x: string]: unknown;
                kind: string;
                id: string;
            } | undefined;
            source_goal_id?: string | undefined;
            target_goal_id?: string | undefined;
            edge_type?: string | undefined;
            label?: string | undefined;
            version?: number | undefined;
            created_at?: string | undefined;
            updated_at?: string | undefined;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        query: {
            expected_version: string | number;
        };
    } | {
        path: {
            spaceID: string;
        } | {
            spaceID: string;
        } | {
            spaceID: string;
            noteID: string;
        } | {
            spaceID: string;
            drawingID: string;
        } | {
            spaceID: string;
            taskID: string;
        } | {
            spaceID: string;
            roadmapID: string;
        } | {
            spaceID: string;
            eventID: string;
        } | {
            spaceID: string;
            threadID: string;
        } | {
            spaceID: string;
            draftID: string;
        } | {
            spaceID: string;
            connectionID: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            provider: string;
        } | {
            spaceID: string;
            noteID: string;
            uploadID: string;
        } | {
            spaceID: string;
            noteID: string;
            assetID: string;
        } | {
            spaceID: string;
            drawingID: string;
            uploadID: string;
        } | {
            spaceID: string;
            drawingID: string;
            assetID: string;
        } | {
            spaceID: string;
            sourceID: string;
        } | {
            spaceID: string;
            definitionID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            milestoneID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            goalID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            nodeID: string;
        } | {
            spaceID: string;
            roadmapID: string;
            edgeID: string;
        };
        body: {
            expected_version: number;
            milestones?: {
                [x: string]: unknown;
                id: string;
                space_id?: string | undefined;
                roadmap_id?: string | undefined;
                title?: string | undefined;
                description?: string | undefined;
                target_date?: string | null | undefined;
                rank?: number | undefined;
                position_x?: number | undefined;
                position_y?: number | undefined;
                width?: number | undefined;
                height?: number | undefined;
                version?: number | undefined;
                goal_total?: number | undefined;
                goal_done?: number | undefined;
                status?: string | undefined;
                created_at?: string | undefined;
                updated_at?: string | undefined;
            }[] | null | undefined;
            goals?: {
                [x: string]: unknown;
                id: string;
                space_id?: string | undefined;
                roadmap_id?: string | undefined;
                milestone_id?: string | undefined;
                title?: string | undefined;
                description?: string | undefined;
                target_date?: string | null | undefined;
                rank?: number | undefined;
                position_x?: number | undefined;
                position_y?: number | undefined;
                manual_completed_at?: string | null | undefined;
                manual_completed_by_user_id?: string | undefined;
                version?: number | undefined;
                task_total?: number | undefined;
                task_done?: number | undefined;
                progress_percentage?: number | undefined;
                status?: string | undefined;
                tasks?: {
                    [x: string]: unknown;
                    id: string;
                    space_id: string;
                    task_number: number;
                    task_key: string;
                    title: string;
                    notes: string;
                    status: string;
                    priority: string;
                    rank: number;
                    due_timezone: string;
                    source_refs: z.core.util.JSONType;
                    audience_kind: string;
                    version: number;
                    created_at: string;
                    updated_at: string;
                    assignee_user_id?: string | undefined;
                    assignee_agent_id?: string | undefined;
                    agent_run?: {
                        [x: string]: unknown;
                        mode?: string | undefined;
                        context_references?: {
                            [x: string]: unknown;
                            device_id: string;
                            kind: string;
                            opaque_ref: string;
                            capabilities: z.core.util.JSONType;
                            display_name?: string | undefined;
                            metadata?: z.core.util.JSONType | undefined;
                        }[] | null | undefined;
                    } | null | undefined;
                    due_at?: string | null | undefined;
                    created_by_user_id?: string | undefined;
                    created_by_agent_id?: string | undefined;
                    source_run_id?: string | undefined;
                    audience_conversation_id?: string | undefined;
                    audience_creator_user_id?: string | undefined;
                    completed_at?: string | null | undefined;
                    archived_at?: string | null | undefined;
                    schedule?: z.core.util.JSONType | undefined;
                    calendar?: z.core.util.JSONType | undefined;
                    conflicted_fields?: string[] | null | undefined;
                }[] | null | undefined;
                created_at?: string | undefined;
                updated_at?: string | undefined;
            }[] | null | undefined;
            nodes?: {
                [x: string]: unknown;
                id: string;
                space_id?: string | undefined;
                roadmap_id?: string | undefined;
                milestone_id?: string | undefined;
                definition_id?: string | undefined;
                node_kind?: string | undefined;
                title?: string | undefined;
                description?: string | undefined;
                target_date?: string | null | undefined;
                position_x?: number | undefined;
                position_y?: number | undefined;
                field_values?: z.core.util.JSONType | undefined;
                version?: number | undefined;
                archived_at?: string | null | undefined;
                created_at?: string | undefined;
                updated_at?: string | undefined;
            }[] | null | undefined;
        };
    };
};
