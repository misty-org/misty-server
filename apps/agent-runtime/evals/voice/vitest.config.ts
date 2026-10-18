import { defineConfig } from "vitest/config";

// The voice evals call Soniox and the Gateway; they never run with the unit tests.
export default defineConfig({
  test: {
    environment: "node",
    include: ["evals/voice/*.eval.ts"],
    testTimeout: 10 * 60_000,
  },
});
