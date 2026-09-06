import { z } from "zod";
import { MailAccountSchema, MailFolderSchema, MailThreadSchema, } from "./mail.js";
export const MISTY_MAIL_CACHE_MAX_BYTES = 32 * 1024 * 1024;
const key = z.string().min(1).max(1024);
const records = (value) => z.record(key, value);
const CacheThreadSchema = MailThreadSchema.extend({ connectionId: key, key });
export const MailCacheDataSchema = z
    .strictObject({
    accounts: z.array(MailAccountSchema),
    foldersByConnection: records(z.array(MailFolderSchema)),
    threadsByConnection: records(z.array(CacheThreadSchema).max(200)),
    nextPageByConnection: records(z.string().optional()),
    estimatedTotalByConnection: records(z.number().int().nonnegative()),
    detailFetchedAtByThread: records(z.number().finite().nonnegative()),
})
    .refine((value) => new TextEncoder().encode(JSON.stringify(value)).length <=
    MISTY_MAIL_CACHE_MAX_BYTES, "The mail cache exceeds 32 MiB.");
export const MailCacheSnapshotSchema = z.strictObject({
    version: z.literal(2),
    accountId: z.string().min(1),
    savedAt: z.iso.datetime(),
    data: MailCacheDataSchema,
});
/** Host-only encrypted storage. Account, deployment, Space and App ownership come from the mounted scope. */
export const mistyMailCacheContracts = {
    "mail.cache.read": {
        params: z.strictObject({}),
        result: MailCacheSnapshotSchema.nullable(),
    },
    "mail.cache.write": {
        params: z.strictObject({ data: MailCacheDataSchema }),
        result: z.undefined(),
    },
    "mail.cache.clear": { params: z.strictObject({}), result: z.undefined() },
};
export const isMistyMailCacheMethod = (method) => Object.hasOwn(mistyMailCacheContracts, method);
//# sourceMappingURL=mail-cache.js.map