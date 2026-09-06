import { configDefaults, defineConfig } from "vitest/config";

const runtimeIntegration = process.env.MISTY_RUNTIME_TEST === "1";

// Pure-logic modules (ticket verification, projection signing, ACL decisions)
// use only standard WebCrypto and run identically under Node and workerd, so
// they are tested here without the Workers pool. Durable Object behaviour is
// covered separately by tests that need the real runtime.
export default defineConfig({
  test: {
    include: ["test/**/*.test.ts"],
    exclude: runtimeIntegration
      ? configDefaults.exclude
      : [...configDefaults.exclude, "test/runtime-lifecycle.test.ts"],
    environment: "node",
  },
});
