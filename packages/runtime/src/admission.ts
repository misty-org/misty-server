import { AsyncLocalStorage } from "node:async_hooks";

/** Reject excess work before allocating large request bodies. Nested in-process
 * dispatch shares its live permit; callers cannot obtain a permit via headers. */
export function createAdmission(limit: number) {
  if (!Number.isInteger(limit) || limit < 1) throw new Error("Invalid admission limit");
  const context = new AsyncLocalStorage<{ active: boolean }>(); let active = 0;
  return {
    run: async <T, U>(operation: () => T | Promise<T>, unavailable: () => U | Promise<U>): Promise<T | U> => {
      if (context.getStore()?.active) return operation();
      if (active >= limit) return unavailable();
      const permit = { active: true }; active++;
      try { return await context.run(permit, operation); }
      finally { permit.active = false; active--; }
    },
  };
}
export type Admission = ReturnType<typeof createAdmission>;
