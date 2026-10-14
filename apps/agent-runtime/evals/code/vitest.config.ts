import { defineConfig } from "vitest/config";

// The code eval drives real models and the native sandbox; it never runs with the unit tests.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/code/*.eval.ts"],
    testTimeout: 6 * 60 * 60_000,
  },
});
