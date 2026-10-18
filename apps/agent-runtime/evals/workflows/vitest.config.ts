import { defineConfig } from "vitest/config";

// The workflow eval drives real models through a local Misty stack; it never runs with the unit tests.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/workflows/*.eval.ts"],
    testTimeout: 6 * 60 * 60_000,
  },
});
