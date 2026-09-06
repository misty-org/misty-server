import { z } from "zod";
export declare const MISTY_CLIPBOARD_PNG_MAX_BYTES: number;
export declare const MistyClipboardPngSchema: z.ZodObject<{
    mimeType: z.ZodLiteral<"image/png">;
    data: z.ZodString;
}, z.core.$strict>;
export declare const mistyClipboardContracts: {
    readonly "clipboard.readImage": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodNullable<z.ZodObject<{
            mimeType: z.ZodLiteral<"image/png">;
            data: z.ZodString;
        }, z.core.$strict>>;
    };
    readonly "clipboard.writeImage": {
        readonly params: z.ZodObject<{
            mimeType: z.ZodLiteral<"image/png">;
            data: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodVoid;
    };
};
