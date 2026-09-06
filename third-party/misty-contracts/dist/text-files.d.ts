import { z } from "zod";
/** Maximum UTF-8 byte length of one text file, matching the native Code editor. */
export declare const MISTY_TEXT_FILE_MAX_BYTES: number;
export declare const mistyTextFileContracts: {
    readonly "files.readText": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            text: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "files.writeText": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            text: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
