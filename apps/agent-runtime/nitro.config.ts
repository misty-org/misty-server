import { defineNitroConfig } from "nitro/config";

export default defineNitroConfig({
  modules: ["workflow/nitro"],
  vercel: { entryFormat: "node" },
  // Live transcription holds a WebSocket per session, so it only runs on the
  // long-lived node-server deployment (Dockerfile), not on Vercel functions.
  features: { websocket: true },
  routes: {
    "/v1/live/transcribe": { handler: "./src/live/route.ts" },
    "/**": { handler: "./src/index.ts", format: "node" },
  },
});
