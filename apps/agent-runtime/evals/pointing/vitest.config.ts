import { defineConfig } from "vitest/config";

// The pointing eval calls real models; it never runs with the unit tests.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/pointing/*.eval.ts"],
    testTimeout: 60 * 60_000,
  },
});
