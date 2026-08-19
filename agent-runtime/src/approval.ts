import { defineHook } from "workflow";
import { z } from "zod";

export const agentToolApprovalHook = defineHook({
  schema: z.object({
    approved: z.boolean(),
    approval_id: z.string().min(1),
  }),
});
