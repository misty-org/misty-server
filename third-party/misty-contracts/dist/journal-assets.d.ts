import { z } from "zod";
export declare const MISTY_JOURNAL_ASSET_MAX_BYTES: number;
export declare const JournalAssetMimeSchema: z.ZodEnum<{
    "image/jpeg": "image/jpeg";
    "image/png": "image/png";
    "image/webp": "image/webp";
    "image/gif": "image/gif";
    "image/avif": "image/avif";
    "image/bmp": "image/bmp";
    "image/x-icon": "image/x-icon";
    "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
}>;
export declare const JournalAssetUploadInputSchema: z.ZodObject<{
    filename: z.ZodString;
    mime_type: z.ZodEnum<{
        "image/jpeg": "image/jpeg";
        "image/png": "image/png";
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
/** Host-only wire descriptors. Downloaded components use journal.assets instead. */
export declare const JournalAssetReservationSchema: z.ZodObject<{
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
export declare const JournalAssetRecordSchema: z.ZodObject<{
    id: z.ZodString;
    mime_type: z.ZodEnum<{
        "image/jpeg": "image/jpeg";
        "image/png": "image/png";
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
export declare const JournalAssetDownloadSchema: z.ZodObject<{
    url: z.ZodURL;
    expires_at: z.ZodISODateTime;
    filename: z.ZodString;
    mime_type: z.ZodEnum<{
        "image/jpeg": "image/jpeg";
        "image/png": "image/png";
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
export declare const mistyJournalAssetServerContracts: {
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
                    "image/jpeg": "image/jpeg";
                    "image/png": "image/png";
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
                    "image/jpeg": "image/jpeg";
                    "image/png": "image/png";
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
                "image/jpeg": "image/jpeg";
                "image/png": "image/png";
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
                    "image/jpeg": "image/jpeg";
                    "image/png": "image/png";
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
                    "image/jpeg": "image/jpeg";
                    "image/png": "image/png";
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
                "image/jpeg": "image/jpeg";
                "image/png": "image/png";
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
};
export declare const MISTY_JOURNAL_ASSET_CHUNK_BYTES: number;
export declare const mistyJournalAssetContracts: {
    readonly "journal.assets.begin": {
        readonly params: z.ZodObject<{
            resource: z.ZodEnum<{
                note: "note";
                drawing: "drawing";
            }>;
            resourceId: z.ZodString;
            filename: z.ZodString;
            mimeType: z.ZodEnum<{
                "image/jpeg": "image/jpeg";
                "image/png": "image/png";
                "image/webp": "image/webp";
                "image/gif": "image/gif";
                "image/avif": "image/avif";
                "image/bmp": "image/bmp";
                "image/x-icon": "image/x-icon";
                "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
            }>;
            bytes: z.ZodNumber;
            externalFileId: z.ZodOptional<z.ZodString>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodUUID;
        }, z.core.$strict>;
    };
    readonly "journal.assets.write": {
        readonly params: z.ZodObject<{
            handle: z.ZodUUID;
            offset: z.ZodNumber;
            data: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "journal.assets.commit": {
        readonly params: z.ZodObject<{
            handle: z.ZodUUID;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            id: z.ZodString;
            mime_type: z.ZodEnum<{
                "image/jpeg": "image/jpeg";
                "image/png": "image/png";
                "image/webp": "image/webp";
                "image/gif": "image/gif";
                "image/avif": "image/avif";
                "image/bmp": "image/bmp";
                "image/x-icon": "image/x-icon";
                "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
            }>;
            byte_size: z.ZodNumber;
            sha256: z.ZodString;
            excalidraw_file_id: z.ZodOptional<z.ZodString>;
        }, z.core.$strip>;
    };
    readonly "journal.assets.open": {
        readonly params: z.ZodObject<{
            resource: z.ZodEnum<{
                note: "note";
                drawing: "drawing";
            }>;
            resourceId: z.ZodString;
            assetId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            handle: z.ZodUUID;
            filename: z.ZodString;
            mimeType: z.ZodEnum<{
                "image/jpeg": "image/jpeg";
                "image/png": "image/png";
                "image/webp": "image/webp";
                "image/gif": "image/gif";
                "image/avif": "image/avif";
                "image/bmp": "image/bmp";
                "image/x-icon": "image/x-icon";
                "image/vnd.microsoft.icon": "image/vnd.microsoft.icon";
            }>;
            bytes: z.ZodNumber;
            sha256: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "journal.assets.read": {
        readonly params: z.ZodObject<{
            handle: z.ZodUUID;
            offset: z.ZodNumber;
            length: z.ZodNumber;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            data: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "journal.assets.close": {
        readonly params: z.ZodObject<{
            handle: z.ZodUUID;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyJournalAssetMethod = keyof typeof mistyJournalAssetContracts;
export type MistyJournalAssetParams<M extends MistyJournalAssetMethod> = z.input<(typeof mistyJournalAssetContracts)[M]["params"]>;
export type MistyJournalAssetResult<M extends MistyJournalAssetMethod> = z.output<(typeof mistyJournalAssetContracts)[M]["result"]>;
export declare function isMistyJournalAssetMethod(method: string): method is MistyJournalAssetMethod;
