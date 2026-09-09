import { z } from "zod";
export declare const MISTY_CAPABILITY_PROTOCOL_VERSION: 1;
export declare const MistyCapabilityNameSchema: z.ZodString;
export declare const MistyProviderIdSchema: z.ZodString;
/** Existing trusted-device records use device_<uuid>; older clients use UUIDs. */
export declare const MistyCapabilityDeviceIdSchema: z.ZodString;
export declare const MistyCapabilityValueSchema: z.ZodJSONSchema;
/** Schemas are data. Remote references and executable extensions are forbidden. */
export declare const MistyCapabilityJsonSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
export declare const MistyCapabilityEffectsSchema: z.ZodObject<{
    kind: z.ZodEnum<{
        read: "read";
        write: "write";
        send: "send";
        execute: "execute";
        destructive: "destructive";
    }>;
    incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
    approval: z.ZodEnum<{
        interactive: "interactive";
        none: "none";
        scoped: "scoped";
    }>;
    retry: z.ZodEnum<{
        never: "never";
        read_only: "read_only";
        idempotent: "idempotent";
        reconcile: "reconcile";
    }>;
}, z.core.$strict>;
export declare const MistyCapabilityDefinitionSchema: z.ZodObject<{
    name: z.ZodString;
    version: z.ZodNumber;
    description: z.ZodString;
    inputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
    outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
    requiredScopes: z.ZodArray<z.ZodString>;
    effects: z.ZodObject<{
        kind: z.ZodEnum<{
            read: "read";
            write: "write";
            send: "send";
            execute: "execute";
            destructive: "destructive";
        }>;
        incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
        approval: z.ZodEnum<{
            interactive: "interactive";
            none: "none";
            scoped: "scoped";
        }>;
        retry: z.ZodEnum<{
            never: "never";
            read_only: "read_only";
            idempotent: "idempotent";
            reconcile: "reconcile";
        }>;
    }, z.core.$strict>;
}, z.core.$strict>;
export declare const MistyCapabilityOriginSchema: z.ZodString;
export declare const MistyProviderRouteSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    kind: z.ZodLiteral<"server">;
    adapter: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"native">;
    adapter: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"browser">;
    adapter: z.ZodOptional<z.ZodString>;
    adapterVersion: z.ZodOptional<z.ZodNumber>;
    origins: z.ZodArray<z.ZodString>;
    hints: z.ZodDefault<z.ZodArray<z.ZodString>>;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"backend">;
    connectionId: z.ZodString;
}, z.core.$strict>, z.ZodObject<{
    kind: z.ZodLiteral<"view">;
    instanceId: z.ZodString;
}, z.core.$strict>], "kind">;
export declare const MistyCapabilityProviderSchema: z.ZodObject<{
    id: z.ZodString;
    version: z.ZodNumber;
    label: z.ZodString;
    route: z.ZodDiscriminatedUnion<[z.ZodObject<{
        kind: z.ZodLiteral<"server">;
        adapter: z.ZodString;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"native">;
        adapter: z.ZodString;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"browser">;
        adapter: z.ZodOptional<z.ZodString>;
        adapterVersion: z.ZodOptional<z.ZodNumber>;
        origins: z.ZodArray<z.ZodString>;
        hints: z.ZodDefault<z.ZodArray<z.ZodString>>;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"backend">;
        connectionId: z.ZodString;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"view">;
        instanceId: z.ZodString;
    }, z.core.$strict>], "kind">;
    capabilities: z.ZodArray<z.ZodObject<{
        name: z.ZodString;
        version: z.ZodNumber;
        description: z.ZodString;
        inputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
        outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
        requiredScopes: z.ZodArray<z.ZodString>;
        effects: z.ZodObject<{
            kind: z.ZodEnum<{
                read: "read";
                write: "write";
                send: "send";
                execute: "execute";
                destructive: "destructive";
            }>;
            incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
            approval: z.ZodEnum<{
                interactive: "interactive";
                none: "none";
                scoped: "scoped";
            }>;
            retry: z.ZodEnum<{
                never: "never";
                read_only: "read_only";
                idempotent: "idempotent";
                reconcile: "reconcile";
            }>;
        }, z.core.$strict>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyCapabilityManifestSchema: z.ZodObject<{
    protocol: z.ZodLiteral<1>;
    providers: z.ZodArray<z.ZodObject<{
        id: z.ZodString;
        version: z.ZodNumber;
        label: z.ZodString;
        route: z.ZodDiscriminatedUnion<[z.ZodObject<{
            kind: z.ZodLiteral<"server">;
            adapter: z.ZodString;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"native">;
            adapter: z.ZodString;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"browser">;
            adapter: z.ZodOptional<z.ZodString>;
            adapterVersion: z.ZodOptional<z.ZodNumber>;
            origins: z.ZodArray<z.ZodString>;
            hints: z.ZodDefault<z.ZodArray<z.ZodString>>;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"backend">;
            connectionId: z.ZodString;
        }, z.core.$strict>, z.ZodObject<{
            kind: z.ZodLiteral<"view">;
            instanceId: z.ZodString;
        }, z.core.$strict>], "kind">;
        capabilities: z.ZodArray<z.ZodObject<{
            name: z.ZodString;
            version: z.ZodNumber;
            description: z.ZodString;
            inputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
            outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
            requiredScopes: z.ZodArray<z.ZodString>;
            effects: z.ZodObject<{
                kind: z.ZodEnum<{
                    read: "read";
                    write: "write";
                    send: "send";
                    execute: "execute";
                    destructive: "destructive";
                }>;
                incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
                approval: z.ZodEnum<{
                    interactive: "interactive";
                    none: "none";
                    scoped: "scoped";
                }>;
                retry: z.ZodEnum<{
                    never: "never";
                    read_only: "read_only";
                    idempotent: "idempotent";
                    reconcile: "reconcile";
                }>;
            }, z.core.$strict>;
        }, z.core.$strict>>;
    }, z.core.$strict>>;
}, z.core.$strict>;
/** An intended account/profile binding; it is not proof of the signed-in identity. */
export declare const MistyBrowserCapabilityBindingSchema: z.ZodObject<{
    kind: z.ZodLiteral<"browser">;
    deviceId: z.ZodString;
    profileId: z.ZodString;
    accountBindingId: z.ZodString;
    origins: z.ZodArray<z.ZodString>;
    contextId: z.ZodOptional<z.ZodString>;
    scopeId: z.ZodOptional<z.ZodString>;
    accountIdentity: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export type MistyBrowserCapabilityBinding = z.output<typeof MistyBrowserCapabilityBindingSchema>;
/** Trusted Misty controls only. This is deliberately not an app RPC method:
 * installing or registering a provider never grants targets or cross-app access. */
export declare const MistyCapabilityTargetConfigurationSchema: z.ZodObject<{
    targetId: z.ZodString;
    expectedRevision: z.ZodNumber;
    providerId: z.ZodString;
    providerVersion: z.ZodNumber;
    spaceId: z.ZodOptional<z.ZodString>;
    label: z.ZodString;
    capabilities: z.ZodArray<z.ZodString>;
    callerApps: z.ZodArray<z.ZodString>;
    browser: z.ZodOptional<z.ZodObject<{
        kind: z.ZodLiteral<"browser">;
        deviceId: z.ZodString;
        profileId: z.ZodString;
        accountBindingId: z.ZodString;
        origins: z.ZodArray<z.ZodString>;
        contextId: z.ZodOptional<z.ZodString>;
        scopeId: z.ZodOptional<z.ZodString>;
        accountIdentity: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>;
export type MistyCapabilityTargetConfiguration = z.output<typeof MistyCapabilityTargetConfigurationSchema>;
/** These identities are issued/verified by Misty; declaring one does not grant access. */
export declare const MistyCapabilityTargetSchema: z.ZodObject<{
    id: z.ZodString;
    revision: z.ZodNumber;
    appId: z.ZodString;
    providerId: z.ZodString;
    providerVersion: z.ZodNumber;
    spaceId: z.ZodOptional<z.ZodString>;
    label: z.ZodString;
    binding: z.ZodDiscriminatedUnion<[z.ZodObject<{
        kind: z.ZodLiteral<"resource">;
        resourceId: z.ZodString;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"browser">;
        deviceId: z.ZodString;
        profileId: z.ZodString;
        accountBindingId: z.ZodString;
        origins: z.ZodArray<z.ZodString>;
        contextId: z.ZodOptional<z.ZodString>;
        scopeId: z.ZodOptional<z.ZodString>;
        accountIdentity: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"native">;
        deviceId: z.ZodString;
        resourceHandle: z.ZodString;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"backend">;
        connectionId: z.ZodString;
    }, z.core.$strict>, z.ZodObject<{
        kind: z.ZodLiteral<"view">;
        deviceId: z.ZodString;
        instanceId: z.ZodString;
    }, z.core.$strict>], "kind">;
}, z.core.$strict>;
/** Trusted control inventory, including disabled targets; existence is not availability. */
export declare const MistyCapabilityTargetPageSchema: z.ZodObject<{
    targets: z.ZodArray<z.ZodObject<{
        target: z.ZodObject<{
            id: z.ZodString;
            revision: z.ZodNumber;
            appId: z.ZodString;
            providerId: z.ZodString;
            providerVersion: z.ZodNumber;
            spaceId: z.ZodOptional<z.ZodString>;
            label: z.ZodString;
            binding: z.ZodDiscriminatedUnion<[z.ZodObject<{
                kind: z.ZodLiteral<"resource">;
                resourceId: z.ZodString;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"browser">;
                deviceId: z.ZodString;
                profileId: z.ZodString;
                accountBindingId: z.ZodString;
                origins: z.ZodArray<z.ZodString>;
                contextId: z.ZodOptional<z.ZodString>;
                scopeId: z.ZodOptional<z.ZodString>;
                accountIdentity: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"native">;
                deviceId: z.ZodString;
                resourceHandle: z.ZodString;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"backend">;
                connectionId: z.ZodString;
            }, z.core.$strict>, z.ZodObject<{
                kind: z.ZodLiteral<"view">;
                deviceId: z.ZodString;
                instanceId: z.ZodString;
            }, z.core.$strict>], "kind">;
        }, z.core.$strict>;
        capabilities: z.ZodArray<z.ZodString>;
        callerApps: z.ZodArray<z.ZodString>;
        enabled: z.ZodBoolean;
    }, z.core.$strict>>;
    nextCursor: z.ZodNullable<z.ZodString>;
}, z.core.$strict>;
export type MistyCapabilityTargetPage = z.output<typeof MistyCapabilityTargetPageSchema>;
export declare const MistyCapabilityAvailabilitySchema: z.ZodObject<{
    state: z.ZodEnum<{
        available: "available";
        device_required: "device_required";
        authentication_required: "authentication_required";
        account_confirmation_required: "account_confirmation_required";
        view_closed: "view_closed";
        unavailable: "unavailable";
        revoked: "revoked";
    }>;
    observedAt: z.ZodISODateTime;
    reason: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export declare const MistyCapabilityEvidenceSchema: z.ZodObject<{
    targetId: z.ZodString;
    observedAt: z.ZodISODateTime;
    kind: z.ZodEnum<{
        browser: "browser";
        provider: "provider";
        resource: "resource";
        command: "command";
    }>;
    reference: z.ZodString;
    revision: z.ZodOptional<z.ZodString>;
    excerpt: z.ZodOptional<z.ZodString>;
}, z.core.$strict>;
export declare const MistyCapabilityOutcomeSchema: z.ZodDiscriminatedUnion<[z.ZodObject<{
    status: z.ZodLiteral<"success">;
    result: z.ZodJSONSchema;
    evidence: z.ZodArray<z.ZodObject<{
        targetId: z.ZodString;
        observedAt: z.ZodISODateTime;
        kind: z.ZodEnum<{
            browser: "browser";
            provider: "provider";
            resource: "resource";
            command: "command";
        }>;
        reference: z.ZodString;
        revision: z.ZodOptional<z.ZodString>;
        excerpt: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
    partial: z.ZodBoolean;
}, z.core.$strict>, z.ZodObject<{
    status: z.ZodLiteral<"failure">;
    code: z.ZodString;
    message: z.ZodString;
    retryable: z.ZodBoolean;
}, z.core.$strict>, z.ZodObject<{
    approvalId: z.ZodString;
    waitId: z.ZodString;
    expiresAt: z.ZodISODateTime;
    reason: z.ZodString;
    status: z.ZodLiteral<"approval_required">;
}, z.core.$strict>, z.ZodObject<{
    deviceId: z.ZodString;
    waitId: z.ZodString;
    expiresAt: z.ZodISODateTime;
    reason: z.ZodString;
    status: z.ZodLiteral<"device_required">;
}, z.core.$strict>, z.ZodObject<{
    action: z.ZodEnum<{
        sign_in: "sign_in";
        account_confirmation: "account_confirmation";
        challenge: "challenge";
        open_target: "open_target";
        review: "review";
    }>;
    waitId: z.ZodString;
    expiresAt: z.ZodISODateTime;
    reason: z.ZodString;
    status: z.ZodLiteral<"user_intervention_required">;
}, z.core.$strict>, z.ZodObject<{
    status: z.ZodLiteral<"uncertain">;
    effectId: z.ZodString;
    reason: z.ZodString;
    evidence: z.ZodArray<z.ZodObject<{
        targetId: z.ZodString;
        observedAt: z.ZodISODateTime;
        kind: z.ZodEnum<{
            browser: "browser";
            provider: "provider";
            resource: "resource";
            command: "command";
        }>;
        reference: z.ZodString;
        revision: z.ZodOptional<z.ZodString>;
        excerpt: z.ZodOptional<z.ZodString>;
    }, z.core.$strict>>;
}, z.core.$strict>], "status">;
export declare const MistyCapabilityInvocationSchema: z.ZodObject<{
    requestId: z.ZodString;
    capability: z.ZodString;
    capabilityVersion: z.ZodNumber;
    providerId: z.ZodString;
    providerVersion: z.ZodNumber;
    targetId: z.ZodString;
    targetRevision: z.ZodNumber;
    input: z.ZodJSONSchema;
    deadline: z.ZodISODateTime;
}, z.core.$strict>;
/** Host-issued execution envelope. App callers cannot select a run or grant identity. */
export declare const MistyCapabilityExecutionSchema: z.ZodObject<{
    requestId: z.ZodString;
    capability: z.ZodString;
    capabilityVersion: z.ZodNumber;
    providerId: z.ZodString;
    providerVersion: z.ZodNumber;
    targetId: z.ZodString;
    targetRevision: z.ZodNumber;
    input: z.ZodJSONSchema;
    deadline: z.ZodISODateTime;
    runId: z.ZodString;
    effectId: z.ZodString;
    grantIds: z.ZodArray<z.ZodString>;
}, z.core.$strict>;
export declare const mistyCapabilityServerContracts: {
    readonly "capabilities.providers.register": {
        readonly verb: "POST";
        readonly path: "/capabilities/providers";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                manifestDigest: z.ZodString;
                provider: z.ZodObject<{
                    id: z.ZodString;
                    version: z.ZodNumber;
                    label: z.ZodString;
                    route: z.ZodDiscriminatedUnion<[z.ZodObject<{
                        kind: z.ZodLiteral<"server">;
                        adapter: z.ZodString;
                    }, z.core.$strict>, z.ZodObject<{
                        kind: z.ZodLiteral<"native">;
                        adapter: z.ZodString;
                    }, z.core.$strict>, z.ZodObject<{
                        kind: z.ZodLiteral<"browser">;
                        adapter: z.ZodOptional<z.ZodString>;
                        adapterVersion: z.ZodOptional<z.ZodNumber>;
                        origins: z.ZodArray<z.ZodString>;
                        hints: z.ZodDefault<z.ZodArray<z.ZodString>>;
                    }, z.core.$strict>, z.ZodObject<{
                        kind: z.ZodLiteral<"backend">;
                        connectionId: z.ZodString;
                    }, z.core.$strict>, z.ZodObject<{
                        kind: z.ZodLiteral<"view">;
                        instanceId: z.ZodString;
                    }, z.core.$strict>], "kind">;
                    capabilities: z.ZodArray<z.ZodObject<{
                        name: z.ZodString;
                        version: z.ZodNumber;
                        description: z.ZodString;
                        inputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                        outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                        requiredScopes: z.ZodArray<z.ZodString>;
                        effects: z.ZodObject<{
                            kind: z.ZodEnum<{
                                read: "read";
                                write: "write";
                                send: "send";
                                execute: "execute";
                                destructive: "destructive";
                            }>;
                            incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
                            approval: z.ZodEnum<{
                                interactive: "interactive";
                                none: "none";
                                scoped: "scoped";
                            }>;
                            retry: z.ZodEnum<{
                                never: "never";
                                read_only: "read_only";
                                idempotent: "idempotent";
                                reconcile: "reconcile";
                            }>;
                        }, z.core.$strict>;
                    }, z.core.$strict>>;
                }, z.core.$strict>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            provider: z.ZodObject<{
                id: z.ZodString;
                version: z.ZodNumber;
                label: z.ZodString;
                route: z.ZodDiscriminatedUnion<[z.ZodObject<{
                    kind: z.ZodLiteral<"server">;
                    adapter: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"native">;
                    adapter: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"browser">;
                    adapter: z.ZodOptional<z.ZodString>;
                    adapterVersion: z.ZodOptional<z.ZodNumber>;
                    origins: z.ZodArray<z.ZodString>;
                    hints: z.ZodDefault<z.ZodArray<z.ZodString>>;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"backend">;
                    connectionId: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"view">;
                    instanceId: z.ZodString;
                }, z.core.$strict>], "kind">;
                capabilities: z.ZodArray<z.ZodObject<{
                    name: z.ZodString;
                    version: z.ZodNumber;
                    description: z.ZodString;
                    inputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                    outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                    requiredScopes: z.ZodArray<z.ZodString>;
                    effects: z.ZodObject<{
                        kind: z.ZodEnum<{
                            read: "read";
                            write: "write";
                            send: "send";
                            execute: "execute";
                            destructive: "destructive";
                        }>;
                        incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
                        approval: z.ZodEnum<{
                            interactive: "interactive";
                            none: "none";
                            scoped: "scoped";
                        }>;
                        retry: z.ZodEnum<{
                            never: "never";
                            read_only: "read_only";
                            idempotent: "idempotent";
                            reconcile: "reconcile";
                        }>;
                    }, z.core.$strict>;
                }, z.core.$strict>>;
            }, z.core.$strict>;
            availability: z.ZodObject<{
                state: z.ZodEnum<{
                    available: "available";
                    device_required: "device_required";
                    authentication_required: "authentication_required";
                    account_confirmation_required: "account_confirmation_required";
                    view_closed: "view_closed";
                    unavailable: "unavailable";
                    revoked: "revoked";
                }>;
                observedAt: z.ZodISODateTime;
                reason: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
        }, z.core.$strict>;
    };
    readonly "capabilities.providers.unregister": {
        readonly verb: "DELETE";
        readonly path: "/capabilities/providers/{providerID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                providerID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "capabilities.providers.availability": {
        readonly verb: "PUT";
        readonly path: "/capabilities/providers/{providerID}/availability";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                providerID: z.ZodString;
            }, z.core.$strict>;
            body: z.ZodObject<{
                state: z.ZodEnum<{
                    available: "available";
                    device_required: "device_required";
                    authentication_required: "authentication_required";
                    account_confirmation_required: "account_confirmation_required";
                    view_closed: "view_closed";
                    unavailable: "unavailable";
                    revoked: "revoked";
                }>;
                observedAt: z.ZodISODateTime;
                reason: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "capabilities.discover": {
        readonly verb: "POST";
        readonly path: "/capabilities/discover";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                capability: z.ZodOptional<z.ZodString>;
                targetId: z.ZodOptional<z.ZodString>;
                cursor: z.ZodOptional<z.ZodString>;
                limit: z.ZodDefault<z.ZodNumber>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            providers: z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                version: z.ZodNumber;
                label: z.ZodString;
                route: z.ZodDiscriminatedUnion<[z.ZodObject<{
                    kind: z.ZodLiteral<"server">;
                    adapter: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"native">;
                    adapter: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"browser">;
                    adapter: z.ZodOptional<z.ZodString>;
                    adapterVersion: z.ZodOptional<z.ZodNumber>;
                    origins: z.ZodArray<z.ZodString>;
                    hints: z.ZodDefault<z.ZodArray<z.ZodString>>;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"backend">;
                    connectionId: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"view">;
                    instanceId: z.ZodString;
                }, z.core.$strict>], "kind">;
                capabilities: z.ZodArray<z.ZodObject<{
                    name: z.ZodString;
                    version: z.ZodNumber;
                    description: z.ZodString;
                    inputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                    outputSchema: z.ZodPreprocess<z.ZodRecord<z.ZodString, z.ZodJSONSchema>>;
                    requiredScopes: z.ZodArray<z.ZodString>;
                    effects: z.ZodObject<{
                        kind: z.ZodEnum<{
                            read: "read";
                            write: "write";
                            send: "send";
                            execute: "execute";
                            destructive: "destructive";
                        }>;
                        incidental: z.ZodDefault<z.ZodArray<z.ZodString>>;
                        approval: z.ZodEnum<{
                            interactive: "interactive";
                            none: "none";
                            scoped: "scoped";
                        }>;
                        retry: z.ZodEnum<{
                            never: "never";
                            read_only: "read_only";
                            idempotent: "idempotent";
                            reconcile: "reconcile";
                        }>;
                    }, z.core.$strict>;
                }, z.core.$strict>>;
            }, z.core.$strict>>;
            nextCursor: z.ZodNullable<z.ZodString>;
        }, z.core.$strict>;
    };
    readonly "capabilities.targets.resolve": {
        readonly verb: "POST";
        readonly path: "/capabilities/targets/resolve";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                capability: z.ZodString;
                targetId: z.ZodOptional<z.ZodString>;
                contextId: z.ZodOptional<z.ZodString>;
                spaceId: z.ZodOptional<z.ZodString>;
                providerId: z.ZodOptional<z.ZodString>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            targets: z.ZodArray<z.ZodObject<{
                id: z.ZodString;
                revision: z.ZodNumber;
                appId: z.ZodString;
                providerId: z.ZodString;
                providerVersion: z.ZodNumber;
                spaceId: z.ZodOptional<z.ZodString>;
                label: z.ZodString;
                binding: z.ZodDiscriminatedUnion<[z.ZodObject<{
                    kind: z.ZodLiteral<"resource">;
                    resourceId: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"browser">;
                    deviceId: z.ZodString;
                    profileId: z.ZodString;
                    accountBindingId: z.ZodString;
                    origins: z.ZodArray<z.ZodString>;
                    contextId: z.ZodOptional<z.ZodString>;
                    scopeId: z.ZodOptional<z.ZodString>;
                    accountIdentity: z.ZodOptional<z.ZodString>;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"native">;
                    deviceId: z.ZodString;
                    resourceHandle: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"backend">;
                    connectionId: z.ZodString;
                }, z.core.$strict>, z.ZodObject<{
                    kind: z.ZodLiteral<"view">;
                    deviceId: z.ZodString;
                    instanceId: z.ZodString;
                }, z.core.$strict>], "kind">;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "capabilities.invoke": {
        readonly verb: "POST";
        readonly path: "/capabilities/invocations";
        readonly params: z.ZodObject<{
            body: z.ZodObject<{
                requestId: z.ZodString;
                capability: z.ZodString;
                capabilityVersion: z.ZodNumber;
                providerId: z.ZodString;
                providerVersion: z.ZodNumber;
                targetId: z.ZodString;
                targetRevision: z.ZodNumber;
                input: z.ZodJSONSchema;
                deadline: z.ZodISODateTime;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            requestId: z.ZodString;
            runId: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "capabilities.result": {
        readonly verb: "GET";
        readonly path: "/capabilities/invocations/{requestID}";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                requestID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            requestId: z.ZodString;
            state: z.ZodEnum<{
                failed: "failed";
                uncertain: "uncertain";
                queued: "queued";
                running: "running";
                waiting: "waiting";
                completed: "completed";
                cancelled: "cancelled";
            }>;
            outcome: z.ZodNullable<z.ZodDiscriminatedUnion<[z.ZodObject<{
                status: z.ZodLiteral<"success">;
                result: z.ZodJSONSchema;
                evidence: z.ZodArray<z.ZodObject<{
                    targetId: z.ZodString;
                    observedAt: z.ZodISODateTime;
                    kind: z.ZodEnum<{
                        browser: "browser";
                        provider: "provider";
                        resource: "resource";
                        command: "command";
                    }>;
                    reference: z.ZodString;
                    revision: z.ZodOptional<z.ZodString>;
                    excerpt: z.ZodOptional<z.ZodString>;
                }, z.core.$strict>>;
                partial: z.ZodBoolean;
            }, z.core.$strict>, z.ZodObject<{
                status: z.ZodLiteral<"failure">;
                code: z.ZodString;
                message: z.ZodString;
                retryable: z.ZodBoolean;
            }, z.core.$strict>, z.ZodObject<{
                approvalId: z.ZodString;
                waitId: z.ZodString;
                expiresAt: z.ZodISODateTime;
                reason: z.ZodString;
                status: z.ZodLiteral<"approval_required">;
            }, z.core.$strict>, z.ZodObject<{
                deviceId: z.ZodString;
                waitId: z.ZodString;
                expiresAt: z.ZodISODateTime;
                reason: z.ZodString;
                status: z.ZodLiteral<"device_required">;
            }, z.core.$strict>, z.ZodObject<{
                action: z.ZodEnum<{
                    sign_in: "sign_in";
                    account_confirmation: "account_confirmation";
                    challenge: "challenge";
                    open_target: "open_target";
                    review: "review";
                }>;
                waitId: z.ZodString;
                expiresAt: z.ZodISODateTime;
                reason: z.ZodString;
                status: z.ZodLiteral<"user_intervention_required">;
            }, z.core.$strict>, z.ZodObject<{
                status: z.ZodLiteral<"uncertain">;
                effectId: z.ZodString;
                reason: z.ZodString;
                evidence: z.ZodArray<z.ZodObject<{
                    targetId: z.ZodString;
                    observedAt: z.ZodISODateTime;
                    kind: z.ZodEnum<{
                        browser: "browser";
                        provider: "provider";
                        resource: "resource";
                        command: "command";
                    }>;
                    reference: z.ZodString;
                    revision: z.ZodOptional<z.ZodString>;
                    excerpt: z.ZodOptional<z.ZodString>;
                }, z.core.$strict>>;
            }, z.core.$strict>], "status">>;
        }, z.core.$strict>;
    };
    readonly "capabilities.cancel": {
        readonly verb: "POST";
        readonly path: "/capabilities/invocations/{requestID}/cancel";
        readonly params: z.ZodObject<{
            path: z.ZodObject<{
                requestID: z.ZodString;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
};
export type MistyCapabilityDefinition = z.output<typeof MistyCapabilityDefinitionSchema>;
export type MistyCapabilityProvider = z.output<typeof MistyCapabilityProviderSchema>;
export type MistyCapabilityManifest = z.output<typeof MistyCapabilityManifestSchema>;
export type MistyCapabilityTarget = z.output<typeof MistyCapabilityTargetSchema>;
export type MistyCapabilityOutcome = z.output<typeof MistyCapabilityOutcomeSchema>;
export type MistyCapabilityExecution = z.output<typeof MistyCapabilityExecutionSchema>;
export type MistyCapabilityInvocation = z.input<typeof MistyCapabilityInvocationSchema>;
