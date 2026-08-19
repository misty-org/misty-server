import { defineHook } from "workflow";
import { z } from "zod";

export const agentDeviceHook = defineHook({
  schema: z.object({
    available: z.boolean(),
  }),
});
