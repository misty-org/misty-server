import { defineConfig } from "vitest/config";

// The browser eval drives real models and Misty's own browser; it never runs with the unit tests.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/browser/*.eval.ts"],
    testTimeout: 6 * 60 * 60_000,
  },
});
