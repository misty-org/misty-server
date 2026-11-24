import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const here = path.dirname(fileURLToPath(import.meta.url));
const sdkModules = path.resolve(here, "../../../misty-sdk/node_modules");

export default defineConfig({
  resolve: {
    alias: [
      { find: /^yjs$/, replacement: path.join(sdkModules, "yjs/dist/yjs.mjs") },
      { find: /^y-protocols\/(.*)$/, replacement: path.join(sdkModules, "y-protocols/$1.js") },
      { find: /^lib0\/(.*)$/, replacement: path.join(sdkModules, "lib0/$1.js") },
    ],
  },
  test: {
    include: ["rooms/**/*.test.ts"],
    globalSetup: ["harness/global_setup.ts"],
    environment: "node",
    pool: "forks",
    fileParallelism: false,
    testTimeout: 120_000,
    hookTimeout: 300_000,
    teardownTimeout: 60_000,
  },
});
