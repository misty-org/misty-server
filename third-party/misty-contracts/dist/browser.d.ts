import { z } from "zod";
export * from "./browser-providers.js";
export declare const MistyBrowserUrlSchema: z.ZodString;
/** A provider account is an app-local identity, never a cookie or an API token. */
export declare const MistyBrowserProviderSchema: z.ZodObject<{
    id: z.ZodEnum<{
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
}, z.core.$strict>;
export type MistyBrowserProvider = z.infer<typeof MistyBrowserProviderSchema>;
/** Viewport CSS pixels. The host clips to the App view and applies native zoom. */
export declare const MistyBrowserBoundsSchema: z.ZodObject<{
    x: z.ZodNumber;
    y: z.ZodNumber;
    width: z.ZodNumber;
    height: z.ZodNumber;
}, z.core.$strict>;
export declare const MistyBrowserHandleSchema: z.ZodString;
/** No arbitrary JavaScript, selectors, credentials, or filesystem paths cross this boundary. */
export declare const MistyBrowserInteractionSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    kind: z.ZodLiteral<"fill">;
    elementRef: z.ZodString;
    text: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"select">;
    elementRef: z.ZodString;
    values: z.ZodArray<z.ZodString>;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"scroll">;
    elementRef: z.ZodOptional<z.ZodString>;
    x: z.ZodNumber;
    y: z.ZodNumber;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"key">;
    elementRef: z.ZodString;
    key: z.ZodEnum<{
        Enter: "Enter";
        Escape: "Escape";
        Tab: "Tab";
        ArrowUp: "ArrowUp";
        ArrowDown: "ArrowDown";
        ArrowLeft: "ArrowLeft";
        ArrowRight: "ArrowRight";
        Home: "Home";
        End: "End";
    }>;
}, z.core.$strict>], "kind">;
/** Native observations identify a local profile, never authenticate an account.
 * Known login pages report required; every other page remains unknown. */
export declare const MistyBrowserTargetObservationSchema: z.ZodObject<{
    scopeId: z.ZodString;
    profileId: z.ZodOptional<z.ZodString>;
    providerId: z.ZodOptional<z.ZodString>;
    origin: z.ZodString;
    authentication: z.ZodEnum<{
        unknown: "unknown";
        required: "required";
    }>;
    accountIdentity: z.ZodLiteral<"unverified">;
    trust: z.ZodLiteral<"host-observation">;
    observedAt: z.ZodString;
}, z.core.$strict>;
export type MistyBrowserTargetObservation = z.infer<typeof MistyBrowserTargetObservationSchema>;
export declare const MistyBrowserInspectionSchema: z.ZodObject<{
    semantic: z.ZodOptional<z.ZodNullable<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>>;
    target: z.ZodOptional<z.ZodObject<{
        scopeId: z.ZodString;
        profileId: z.ZodOptional<z.ZodString>;
        providerId: z.ZodOptional<z.ZodString>;
        origin: z.ZodString;
        authentication: z.ZodEnum<{
            unknown: "unknown";
            required: "required";
        }>;
        accountIdentity: z.ZodLiteral<"unverified">;
        trust: z.ZodLiteral<"host-observation">;
        observedAt: z.ZodString;
    }, z.core.$strict>>;
    documentId: z.ZodString;
    url: z.ZodString;
    title: z.ZodString;
    text: z.ZodString;
    truncated: z.ZodBoolean;
    interactive: z.ZodArray<z.ZodObject<{
        ref: z.ZodString;
        tag: z.ZodString;
        role: z.ZodString;
        name: z.ZodString;
    }, z.core.$strict>>;
    contentTrust: z.ZodLiteral<"untrusted-web-page">;
}, z.core.$strict>;
export declare const MistyBrowserEventSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    type: z.ZodLiteral<"page">;
    phase: z.ZodEnum<{
        started: "started";
        finished: "finished";
    }>;
    url: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"title">;
    title: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"favicon">;
    url: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"compatibility">;
    kind: z.ZodLiteral<"cloudflare_challenge">;
    url: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"layout">;
}, z.core.$strict>, z.ZodObject<{
    type: z.ZodLiteral<"state">;
    canBack: z.ZodBoolean;
    canForward: z.ZodBoolean;
    loading: z.ZodBoolean;
    agentAccess: z.ZodBoolean;
    history: z.ZodArray<z.ZodString>;
    error: z.ZodNullable<z.ZodString>;
    notice: z.ZodNullable<z.ZodString>;
}, z.core.$strict>], "type">;
export declare const mistyBrowserContracts: {
    readonly "browser.availability": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{}, z.core.$strict>;
        result: z.ZodObject<{
            available: z.ZodBoolean;
            reason: z.ZodOptional<z.ZodString>;
            persistent: z.ZodBoolean;
            supportedProviders: z.ZodOptional<z.ZodArray<z.ZodEnum<{
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
            }>>>;
            profileCleanup: z.ZodOptional<z.ZodBoolean>;
        }, z.core.$strict>;
    };
    readonly "browser.removeAccount": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            provider: z.ZodObject<{
                id: z.ZodEnum<{
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
            }, z.core.$strict>;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.create": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            url: z.ZodOptional<z.ZodString>;
            provider: z.ZodOptional<z.ZodObject<{
                id: z.ZodEnum<{
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
            }, z.core.$strict>>;
            bounds: z.ZodObject<{
                x: z.ZodNumber;
                y: z.ZodNumber;
                width: z.ZodNumber;
                height: z.ZodNumber;
            }, z.core.$strict>;
            nativeLiveResize: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        result: z.ZodObject<{
            handle: z.ZodString;
            contextId: z.ZodString;
            url: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "browser.layout": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            bounds: z.ZodObject<{
                x: z.ZodNumber;
                y: z.ZodNumber;
                width: z.ZodNumber;
                height: z.ZodNumber;
            }, z.core.$strict>;
            visible: z.ZodBoolean;
            nativeLiveResize: z.ZodDefault<z.ZodBoolean>;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.navigate": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            url: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.back": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.forward": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.reload": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.setZoom": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            factor: z.ZodNumber;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.close": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.inspect": {
        readonly capability: "browser.inspect";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            semantic: z.ZodOptional<z.ZodNullable<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>>;
            target: z.ZodOptional<z.ZodObject<{
                scopeId: z.ZodString;
                profileId: z.ZodOptional<z.ZodString>;
                providerId: z.ZodOptional<z.ZodString>;
                origin: z.ZodString;
                authentication: z.ZodEnum<{
                    unknown: "unknown";
                    required: "required";
                }>;
                accountIdentity: z.ZodLiteral<"unverified">;
                trust: z.ZodLiteral<"host-observation">;
                observedAt: z.ZodString;
            }, z.core.$strict>>;
            documentId: z.ZodString;
            url: z.ZodString;
            title: z.ZodString;
            text: z.ZodString;
            truncated: z.ZodBoolean;
            interactive: z.ZodArray<z.ZodObject<{
                ref: z.ZodString;
                tag: z.ZodString;
                role: z.ZodString;
                name: z.ZodString;
            }, z.core.$strict>>;
            contentTrust: z.ZodLiteral<"untrusted-web-page">;
        }, z.core.$strict>;
    };
    readonly "browser.click": {
        readonly capability: "browser.interact";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            documentId: z.ZodString;
            elementRef: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "browser.interact": {
        readonly capability: "browser.interact";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            documentId: z.ZodString;
            action: z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"fill">;
                elementRef: z.ZodString;
                text: z.ZodString;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"select">;
                elementRef: z.ZodString;
                values: z.ZodArray<z.ZodString>;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"scroll">;
                elementRef: z.ZodOptional<z.ZodString>;
                x: z.ZodNumber;
                y: z.ZodNumber;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"key">;
                elementRef: z.ZodString;
                key: z.ZodEnum<{
                    Enter: "Enter";
                    Escape: "Escape";
                    Tab: "Tab";
                    ArrowUp: "ArrowUp";
                    ArrowDown: "ArrowDown";
                    ArrowLeft: "ArrowLeft";
                    ArrowRight: "ArrowRight";
                    Home: "Home";
                    End: "End";
                }>;
            }, z.core.$strict>], "kind">;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            attempted: z.ZodLiteral<true>;
        }, z.core.$strict>;
    };
    readonly "browser.type": {
        readonly capability: "browser.interact";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            documentId: z.ZodString;
            elementRef: z.ZodString;
            text: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            prepared: z.ZodLiteral<true>;
        }, z.core.$strict>;
    };
    readonly "browser.request": {
        readonly capability: "browser.inspect";
        readonly platforms: readonly ["macos"];
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            path: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            status: z.ZodNumber;
            body: z.ZodString;
            truncated: z.ZodBoolean;
        }, z.core.$strict>;
    };
    readonly "browser.overlay": {
        capability: "browser.navigate";
        platforms: readonly ["macos"];
        params: z.ZodObject<{
            handle: z.ZodString;
            reason: z.ZodString;
            active: z.ZodBoolean;
        }, z.core.$strict>;
        result: z.ZodPipe<z.ZodUnion<readonly [z.ZodUndefined, z.ZodNull]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyBrowserMethod = keyof typeof mistyBrowserContracts;
export type MistyBrowserParams<M extends MistyBrowserMethod> = z.input<(typeof mistyBrowserContracts)[M]["params"]>;
export type MistyBrowserResult<M extends MistyBrowserMethod> = z.output<(typeof mistyBrowserContracts)[M]["result"]>;
export type MistyBrowserEvent = z.infer<typeof MistyBrowserEventSchema>;
export type MistyBrowserBounds = z.infer<typeof MistyBrowserBoundsSchema>;
export type MistyBrowserInteraction = z.infer<typeof MistyBrowserInteractionSchema>;
export type MistyBrowserInspection = z.infer<typeof MistyBrowserInspectionSchema>;
export declare function isMistyBrowserMethod(method: string): method is MistyBrowserMethod;
