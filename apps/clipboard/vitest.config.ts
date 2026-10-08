import { defineConfig } from "vitest/config";

// Ticket checks and clip rules use only standard WebCrypto, so they run under
// Node without the Workers pool.
export default defineConfig({
  test: {
    include: ["test/**/*.test.ts"],
    environment: "node",
  },
});
