import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    projects: [
      {
        test: {
          name: "unit",
          environment: "node",
          include: ["apps/payments/src/**/*.test.ts", "packages/**/*.test.ts"],
          exclude: ["**/*.integration.test.ts"],
          restoreMocks: true,
        },
      },
      {
        test: {
          name: "integration",
          environment: "node",
          include: ["apps/payments/src/**/*.integration.test.ts", "packages/**/*.integration.test.ts"],
          fileParallelism: false,
          testTimeout: 15000,
        },
      },
    ],
  },
});
