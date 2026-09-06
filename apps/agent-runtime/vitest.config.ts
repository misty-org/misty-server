import { defineConfig } from "vitest/config";

// This independently deployed application must not inherit the API test projects.
export default defineConfig({
  test: { environment: "node", include: ["test/**/*.test.ts"] },
});
