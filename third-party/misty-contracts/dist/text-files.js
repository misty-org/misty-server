import { z } from "zod";
/** Maximum UTF-8 byte length of one text file, matching the native Code editor. */
export const MISTY_TEXT_FILE_MAX_BYTES = 5 * 1024 * 1024;
const encoder = new TextEncoder();
const text = z
    .string()
    .max(MISTY_TEXT_FILE_MAX_BYTES)
    .refine((value) => encoder.encode(value).byteLength <= MISTY_TEXT_FILE_MAX_BYTES, "Text files are limited to 5 MiB of UTF-8 text.");
const handle = z.string().min(1).max(256);
export const mistyTextFileContracts = {
    "files.readText": {
        params: z.strictObject({ handle }),
        result: z.strictObject({ text }),
    },
    "files.writeText": {
        params: z.strictObject({ handle, text }),
        result: z.union([z.null(), z.undefined()]).transform(() => undefined),
    },
};
//# sourceMappingURL=text-files.js.map