import { defineConfig } from "vitest/config";

// Checks a free eval model on its own, before a benchmark pass spends a day's rate limit on it.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/free-model/*.eval.ts"],
    testTimeout: 5 * 60_000,
  },
});
