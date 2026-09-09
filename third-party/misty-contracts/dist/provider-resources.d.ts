import { z } from "zod";
export declare const MistyProviderResourceKindSchema: z.ZodEnum<{
    file: "file";
    conversation: "conversation";
    task: "task";
    document: "document";
    event: "event";
}>;
export declare const MistyProviderDestinationSchema: z.ZodEnum<{
    planner: "planner";
    chat: "chat";
    library: "library";
    journal: "journal";
}>;
export declare const MistyProviderSyncStateSchema: z.ZodEnum<{
    device_required: "device_required";
    authentication_required: "authentication_required";
    partial: "partial";
    snapshot: "snapshot";
    pending: "pending";
    syncing: "syncing";
    live: "live";
    periodic: "periodic";
    delayed: "delayed";
    paused: "paused";
    source_deleted: "source_deleted";
    access_lost: "access_lost";
    left_container: "left_container";
    stopped: "stopped";
}>;
export declare const MistyProviderResourceSchema: z.ZodObject<{
    id: z.ZodString;
    kind: z.ZodEnum<{
        file: "file";
        conversation: "conversation";
        task: "task";
        document: "document";
        event: "event";
    }>;
    title: z.ZodString;
    url: z.ZodURL;
    revision: z.ZodString;
    observedAt: z.ZodISODateTime;
    text: z.ZodDefault<z.ZodString>;
    partial: z.ZodBoolean;
    limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
}, z.core.$strict>;
export declare const MistyProviderSourceSchema: z.ZodObject<{
    provider: z.ZodEnum<{
        "google-drive": "google-drive";
        dropbox: "dropbox";
        onedrive: "onedrive";
        google: "google";
        microsoft: "microsoft";
        instagram: "instagram";
        messenger: "messenger";
        x: "x";
        discord: "discord";
        slack: "slack";
        "microsoft-teams": "microsoft-teams";
        icloud: "icloud";
        yahoo: "yahoo";
        "google-docs": "google-docs";
        "microsoft-word": "microsoft-word";
        notion: "notion";
        "microsoft-onenote": "microsoft-onenote";
        "google-calendar": "google-calendar";
        "outlook-calendar": "outlook-calendar";
        "microsoft-todo": "microsoft-todo";
        todoist: "todoist";
        trello: "trello";
        asana: "asana";
        jira: "jira";
    }>;
    accountId: z.ZodString;
    resourceId: z.ZodString;
    selection: z.ZodEnum<{
        item: "item";
        container: "container";
    }>;
    includeFutureItems: z.ZodDefault<z.ZodBoolean>;
}, z.core.$strict>;
/** Shared projections deliberately exclude source account IDs, device IDs and sync cursors. */
export declare const MistySharedSourceSchema: z.ZodObject<{
    id: z.ZodString;
    spaceId: z.ZodString;
    destination: z.ZodEnum<{
        planner: "planner";
        chat: "chat";
        library: "library";
        journal: "journal";
    }>;
    provider: z.ZodString;
    contributorId: z.ZodString;
    contributorName: z.ZodDefault<z.ZodString>;
    resource: z.ZodObject<{
        id: z.ZodString;
        kind: z.ZodEnum<{
            file: "file";
            conversation: "conversation";
            task: "task";
            document: "document";
            event: "event";
        }>;
        title: z.ZodString;
        url: z.ZodURL;
        revision: z.ZodString;
        observedAt: z.ZodISODateTime;
        text: z.ZodDefault<z.ZodString>;
        partial: z.ZodBoolean;
        limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
    }, z.core.$strict>;
    state: z.ZodEnum<{
        device_required: "device_required";
        authentication_required: "authentication_required";
        partial: "partial";
        snapshot: "snapshot";
        pending: "pending";
        syncing: "syncing";
        live: "live";
        periodic: "periodic";
        delayed: "delayed";
        paused: "paused";
        source_deleted: "source_deleted";
        access_lost: "access_lost";
        left_container: "left_container";
        stopped: "stopped";
    }>;
    syncMode: z.ZodDefault<z.ZodEnum<{
        push: "push";
        none: "none";
        device: "device";
        poll: "poll";
    }>>;
    version: z.ZodNumber;
    updatedAt: z.ZodISODateTime;
    lastSuccessAt: z.ZodNullable<z.ZodISODateTime>;
    canManage: z.ZodBoolean;
}, z.core.$strict>;
export declare const MistyProviderFeatureSchema: z.ZodEnum<{
    search: "search";
    read: "read";
    export: "export";
    sync: "sync";
    actions: "actions";
}>;
export declare const MistyProviderAvailabilitySchema: z.ZodObject<{
    provider: z.ZodString;
    features: z.ZodArray<z.ZodEnum<{
        search: "search";
        read: "read";
        export: "export";
        sync: "sync";
        actions: "actions";
    }>>;
    syncMode: z.ZodEnum<{
        push: "push";
        none: "none";
        device: "device";
        poll: "poll";
    }>;
    reason: z.ZodString;
}, z.core.$strict>;
export declare const mistyProviderResourceContracts: {
    readonly "sources.availability": {
        readonly verb: "GET";
        readonly path: "/provider-sources/availability";
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            providers: z.ZodArray<z.ZodObject<{
                provider: z.ZodString;
                features: z.ZodArray<z.ZodEnum<{
                    search: "search";
                    read: "read";
                    export: "export";
                    sync: "sync";
                    actions: "actions";
                }>>;
                syncMode: z.ZodEnum<{
                    push: "push";
                    none: "none";
                    device: "device";
                    poll: "poll";
                }>;
                reason: z.ZodString;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "sources.list": {
        readonly verb: "GET";
        readonly path: "/spaces/{spaceID}/sources";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            sources: z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                spaceId: z.ZodString;
                destination: z.ZodEnum<{
                    planner: "planner";
                    chat: "chat";
                    library: "library";
                    journal: "journal";
                }>;
                provider: z.ZodString;
                contributorId: z.ZodString;
                contributorName: z.ZodDefault<z.ZodString>;
                resource: z.ZodObject<{
                    id: z.ZodString;
                    kind: z.ZodEnum<{
                        file: "file";
                        conversation: "conversation";
                        task: "task";
                        document: "document";
                        event: "event";
                    }>;
                    title: z.ZodString;
                    url: z.ZodURL;
                    revision: z.ZodString;
                    observedAt: z.ZodISODateTime;
                    text: z.ZodDefault<z.ZodString>;
                    partial: z.ZodBoolean;
                    limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
                }, z.core.$strict>;
                state: z.ZodEnum<{
                    device_required: "device_required";
                    authentication_required: "authentication_required";
                    partial: "partial";
                    snapshot: "snapshot";
                    pending: "pending";
                    syncing: "syncing";
                    live: "live";
                    periodic: "periodic";
                    delayed: "delayed";
                    paused: "paused";
                    source_deleted: "source_deleted";
                    access_lost: "access_lost";
                    left_container: "left_container";
                    stopped: "stopped";
                }>;
                syncMode: z.ZodDefault<z.ZodEnum<{
                    push: "push";
                    none: "none";
                    device: "device";
                    poll: "poll";
                }>>;
                version: z.ZodNumber;
                updatedAt: z.ZodISODateTime;
                lastSuccessAt: z.ZodNullable<z.ZodISODateTime>;
                canManage: z.ZodBoolean;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "sources.preview": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/sources/preview";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                source: z.ZodObject<{
                    provider: z.ZodEnum<{
                        "google-drive": "google-drive";
                        dropbox: "dropbox";
                        onedrive: "onedrive";
                        google: "google";
                        microsoft: "microsoft";
                        instagram: "instagram";
                        messenger: "messenger";
                        x: "x";
                        discord: "discord";
                        slack: "slack";
                        "microsoft-teams": "microsoft-teams";
                        icloud: "icloud";
                        yahoo: "yahoo";
                        "google-docs": "google-docs";
                        "microsoft-word": "microsoft-word";
                        notion: "notion";
                        "microsoft-onenote": "microsoft-onenote";
                        "google-calendar": "google-calendar";
                        "outlook-calendar": "outlook-calendar";
                        "microsoft-todo": "microsoft-todo";
                        todoist: "todoist";
                        trello: "trello";
                        asana: "asana";
                        jira: "jira";
                    }>;
                    accountId: z.ZodString;
                    resourceId: z.ZodString;
                    selection: z.ZodEnum<{
                        item: "item";
                        container: "container";
                    }>;
                    includeFutureItems: z.ZodDefault<z.ZodBoolean>;
                }, z.core.$strict>;
                destination: z.ZodEnum<{
                    planner: "planner";
                    chat: "chat";
                    library: "library";
                    journal: "journal";
                }>;
                resource: z.ZodObject<{
                    id: z.ZodString;
                    kind: z.ZodEnum<{
                        file: "file";
                        conversation: "conversation";
                        task: "task";
                        document: "document";
                        event: "event";
                    }>;
                    title: z.ZodString;
                    url: z.ZodURL;
                    revision: z.ZodString;
                    observedAt: z.ZodISODateTime;
                    text: z.ZodDefault<z.ZodString>;
                    partial: z.ZodBoolean;
                    limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
                }, z.core.$strict>;
                updates: z.ZodBoolean;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            token: z.ZodString;
            expiresAt: z.ZodISODateTime;
            audience: z.ZodString;
            resource: z.ZodObject<{
                id: z.ZodString;
                kind: z.ZodEnum<{
                    file: "file";
                    conversation: "conversation";
                    task: "task";
                    document: "document";
                    event: "event";
                }>;
                title: z.ZodString;
                url: z.ZodURL;
                revision: z.ZodString;
                observedAt: z.ZodISODateTime;
                text: z.ZodDefault<z.ZodString>;
                partial: z.ZodBoolean;
                limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
            }, z.core.$strict>;
        }, z.core.$strict>;
    };
    readonly "sources.create": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/sources";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                spaceID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                token: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            spaceId: z.ZodString;
            destination: z.ZodEnum<{
                planner: "planner";
                chat: "chat";
                library: "library";
                journal: "journal";
            }>;
            provider: z.ZodString;
            contributorId: z.ZodString;
            contributorName: z.ZodDefault<z.ZodString>;
            resource: z.ZodObject<{
                id: z.ZodString;
                kind: z.ZodEnum<{
                    file: "file";
                    conversation: "conversation";
                    task: "task";
                    document: "document";
                    event: "event";
                }>;
                title: z.ZodString;
                url: z.ZodURL;
                revision: z.ZodString;
                observedAt: z.ZodISODateTime;
                text: z.ZodDefault<z.ZodString>;
                partial: z.ZodBoolean;
                limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
            }, z.core.$strict>;
            state: z.ZodEnum<{
                device_required: "device_required";
                authentication_required: "authentication_required";
                partial: "partial";
                snapshot: "snapshot";
                pending: "pending";
                syncing: "syncing";
                live: "live";
                periodic: "periodic";
                delayed: "delayed";
                paused: "paused";
                source_deleted: "source_deleted";
                access_lost: "access_lost";
                left_container: "left_container";
                stopped: "stopped";
            }>;
            syncMode: z.ZodDefault<z.ZodEnum<{
                push: "push";
                none: "none";
                device: "device";
                poll: "poll";
            }>>;
            version: z.ZodNumber;
            updatedAt: z.ZodISODateTime;
            lastSuccessAt: z.ZodNullable<z.ZodISODateTime>;
            canManage: z.ZodBoolean;
        }, z.core.$strict>;
    };
    readonly "sources.copy": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/sources/{sourceID}/copy";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                sourceID: z.ZodString;
                spaceID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                version: z.ZodNumber;
                requestId: z.ZodUUID;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            route: z.ZodString;
            id: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "sources.control": {
        readonly verb: "POST";
        readonly path: "/spaces/{spaceID}/sources/{sourceID}/control";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                sourceID: z.ZodString;
                spaceID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                action: z.ZodEnum<{
                    refresh: "refresh";
                    pause: "pause";
                    resume: "resume";
                    stop: "stop";
                }>;
                version: z.ZodNumber;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            spaceId: z.ZodString;
            destination: z.ZodEnum<{
                planner: "planner";
                chat: "chat";
                library: "library";
                journal: "journal";
            }>;
            provider: z.ZodString;
            contributorId: z.ZodString;
            contributorName: z.ZodDefault<z.ZodString>;
            resource: z.ZodObject<{
                id: z.ZodString;
                kind: z.ZodEnum<{
                    file: "file";
                    conversation: "conversation";
                    task: "task";
                    document: "document";
                    event: "event";
                }>;
                title: z.ZodString;
                url: z.ZodURL;
                revision: z.ZodString;
                observedAt: z.ZodISODateTime;
                text: z.ZodDefault<z.ZodString>;
                partial: z.ZodBoolean;
                limitations: z.ZodDefault<z.ZodArray<z.ZodString>>;
            }, z.core.$strict>;
            state: z.ZodEnum<{
                device_required: "device_required";
                authentication_required: "authentication_required";
                partial: "partial";
                snapshot: "snapshot";
                pending: "pending";
                syncing: "syncing";
                live: "live";
                periodic: "periodic";
                delayed: "delayed";
                paused: "paused";
                source_deleted: "source_deleted";
                access_lost: "access_lost";
                left_container: "left_container";
                stopped: "stopped";
            }>;
            syncMode: z.ZodDefault<z.ZodEnum<{
                push: "push";
                none: "none";
                device: "device";
                poll: "poll";
            }>>;
            version: z.ZodNumber;
            updatedAt: z.ZodISODateTime;
            lastSuccessAt: z.ZodNullable<z.ZodISODateTime>;
            canManage: z.ZodBoolean;
        }, z.core.$strict>;
    };
};
export type MistyProviderResource = z.output<typeof MistyProviderResourceSchema>;
export type MistySharedSource = z.output<typeof MistySharedSourceSchema>;
export type MistyProviderSource = z.output<typeof MistyProviderSourceSchema>;
export type MistyProviderSyncState = z.output<typeof MistyProviderSyncStateSchema>;
export type MistyProviderAvailability = z.output<typeof MistyProviderAvailabilitySchema>;
export declare const mistyProviderDestinations: {
    readonly file: "library";
    readonly document: "journal";
    readonly task: "planner";
    readonly event: "planner";
    readonly conversation: "chat";
};
/** Availability is evidence, never inferred from a provider's presence in the catalog. */
export declare function providerSupports(availability: MistyProviderAvailability | undefined, feature: z.infer<typeof MistyProviderFeatureSchema>): boolean;
