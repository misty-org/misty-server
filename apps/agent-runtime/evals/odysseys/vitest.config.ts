import { defineConfig } from "vitest/config";

// The Odysseys eval drives real models through a local Misty stack; it never runs with the unit tests.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/odysseys/*.eval.ts"],
    testTimeout: 6 * 60 * 60_000,
  },
});
