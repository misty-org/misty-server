import { configDefaults, defineConfig } from "vitest/config";

const runtimeIntegration = process.env.MISTY_RUNTIME_TEST === "1";

export default defineConfig({
  test: {
    include: ["test/**/*.test.ts"],
    exclude: runtimeIntegration
      ? configDefaults.exclude
      : [...configDefaults.exclude, "test/runtime-*.test.ts"],
    environment: "node",
  },
});
