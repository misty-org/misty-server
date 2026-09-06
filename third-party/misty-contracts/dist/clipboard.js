import { z } from "zod";
export const MISTY_CLIPBOARD_PNG_MAX_BYTES = 4 * 1024 * 1024;
export const MistyClipboardPngSchema = z.strictObject({
    mimeType: z.literal("image/png"),
    data: z.string().min(4).max(4 * Math.ceil(MISTY_CLIPBOARD_PNG_MAX_BYTES / 3))
        .regex(/^[A-Za-z0-9+/]+={0,2}$/),
});
export const mistyClipboardContracts = {
    "clipboard.readImage": { params: z.strictObject({}), result: MistyClipboardPngSchema.nullable() },
    "clipboard.writeImage": {
        params: MistyClipboardPngSchema,
        result: z.void(),
    },
};
//# sourceMappingURL=clipboard.js.map