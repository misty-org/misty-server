import { z } from "zod";
export declare const MistyArchiveFormatSchema: z.ZodEnum<{
    zip: "zip";
    tar: "tar";
    "7z": "7z";
    rar: "rar";
}>;
export declare const MistyArchivePreviewSchema: z.ZodObject<{
    format: z.ZodEnum<{
        zip: "zip";
        tar: "tar";
        "7z": "7z";
        rar: "rar";
    }>;
    entries: z.ZodArray<z.ZodObject<{
        path: z.ZodString;
        isDir: z.ZodBoolean;
        compressedSize: z.ZodNumber;
        uncompressedSize: z.ZodNumber;
    }, z.core.$strict>>;
}, z.core.$strict>;
export type MistyArchivePreview = z.infer<typeof MistyArchivePreviewSchema>;
export type MistyArchiveFormat = z.infer<typeof MistyArchiveFormatSchema>;
export declare const mistyFilePreviewContracts: {
    readonly "files.listArchive": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            format: z.ZodEnum<{
                zip: "zip";
                tar: "tar";
                "7z": "7z";
                rar: "rar";
            }>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            format: z.ZodEnum<{
                zip: "zip";
                tar: "tar";
                "7z": "7z";
                rar: "rar";
            }>;
            entries: z.ZodArray<z.ZodObject<{
                path: z.ZodString;
                isDir: z.ZodBoolean;
                compressedSize: z.ZodNumber;
                uncompressedSize: z.ZodNumber;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
};
