import { z } from "zod";
export declare const MISTY_MAIL_CACHE_MAX_BYTES: number;
export declare const MailCacheDataSchema: z.ZodObject<{
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
    foldersByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
    }, z.core.$strip>>>;
    threadsByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
        connectionId: z.ZodString;
        key: z.ZodString;
    }, z.core.$strip>>>;
    nextPageByConnection: z.ZodRecord<z.ZodString, z.ZodOptional<z.ZodString>>;
    estimatedTotalByConnection: z.ZodRecord<z.ZodString, z.ZodNumber>;
    detailFetchedAtByThread: z.ZodRecord<z.ZodString, z.ZodNumber>;
}, z.core.$strict>;
export declare const MailCacheSnapshotSchema: z.ZodObject<{
    version: z.ZodLiteral<2>;
    accountId: z.ZodString;
    savedAt: z.ZodISODateTime;
    data: z.ZodObject<{
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
        foldersByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
        }, z.core.$strip>>>;
        threadsByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
            connectionId: z.ZodString;
            key: z.ZodString;
        }, z.core.$strip>>>;
        nextPageByConnection: z.ZodRecord<z.ZodString, z.ZodOptional<z.ZodString>>;
        estimatedTotalByConnection: z.ZodRecord<z.ZodString, z.ZodNumber>;
        detailFetchedAtByThread: z.ZodRecord<z.ZodString, z.ZodNumber>;
    }, z.core.$strict>;
}, z.core.$strict>;
/** Host-only encrypted storage. Account, deployment, Space and App ownership come from the mounted scope. */
export declare const mistyMailCacheContracts: {
    readonly "mail.cache.read": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodNullable<z.ZodObject<{
            version: z.ZodLiteral<2>;
            accountId: z.ZodString;
            savedAt: z.ZodISODateTime;
            data: z.ZodObject<{
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
                foldersByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
                }, z.core.$strip>>>;
                threadsByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
                    connectionId: z.ZodString;
                    key: z.ZodString;
                }, z.core.$strip>>>;
                nextPageByConnection: z.ZodRecord<z.ZodString, z.ZodOptional<z.ZodString>>;
                estimatedTotalByConnection: z.ZodRecord<z.ZodString, z.ZodNumber>;
                detailFetchedAtByThread: z.ZodRecord<z.ZodString, z.ZodNumber>;
            }, z.core.$strict>;
        }, z.core.$strict>>;
    };
    readonly "mail.cache.write": {
        readonly params: z.ZodObject<{
            data: z.ZodObject<{
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
                foldersByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
                }, z.core.$strip>>>;
                threadsByConnection: z.ZodRecord<z.ZodString, z.ZodArray<z.ZodObject<{
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
                    connectionId: z.ZodString;
                    key: z.ZodString;
                }, z.core.$strip>>>;
                nextPageByConnection: z.ZodRecord<z.ZodString, z.ZodOptional<z.ZodString>>;
                estimatedTotalByConnection: z.ZodRecord<z.ZodString, z.ZodNumber>;
                detailFetchedAtByThread: z.ZodRecord<z.ZodString, z.ZodNumber>;
            }, z.core.$strict>;
        }, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
    readonly "mail.cache.clear": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodUndefined;
    };
};
export type MistyMailCacheMethod = keyof typeof mistyMailCacheContracts;
export type MailCacheData = z.input<typeof MailCacheDataSchema>;
export type MailCacheSnapshot = z.output<typeof MailCacheSnapshotSchema>;
export declare const isMistyMailCacheMethod: (method: string) => method is MistyMailCacheMethod;
