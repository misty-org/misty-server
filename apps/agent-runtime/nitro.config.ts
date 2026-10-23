import { defineNitroConfig } from "nitro/config";

export default defineNitroConfig({
  modules: ["workflow/nitro"],
  // Live transcription holds a WebSocket per session on the long-lived
  // node-server deployment (Dockerfile).
  features: { websocket: true },
  routes: {
    "/v1/live/transcribe": { handler: "./src/live/route.ts" },
    "/**": { handler: "./src/index.ts", format: "node" },
  },
});
