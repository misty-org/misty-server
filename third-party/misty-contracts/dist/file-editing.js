import { z } from "zod";
const handle = z.string().min(1).max(256);
const done = z.union([z.null(), z.undefined()]).transform(() => undefined);
export const mistyFileEditingContracts = {
    "files.replaceCopy": {
        params: z.strictObject({ handle, target: handle }),
        result: done,
    },
    "files.openExternal": {
        params: z.strictObject({ handle }),
        result: done,
    },
};
//# sourceMappingURL=file-editing.js.map