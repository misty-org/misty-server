import { z } from "zod";
export declare const mistyFileEditingContracts: {
    readonly "files.replaceCopy": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
            target: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "files.openExternal": {
        readonly params: z.ZodObject<{
            handle: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
