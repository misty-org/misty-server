import { defineNitroConfig } from "nitro/config";

export default defineNitroConfig({
  modules: ["workflow/nitro"],
  // Live transcription and the companion's voice hold a WebSocket per session.
  features: { websocket: true },
  routes: {
    "/v1/live/transcribe": { handler: "./src/live/route.ts" },
    "/v1/device/voice": { handler: "./src/voice/route.ts" },
    "/**": { handler: "./src/index.ts", format: "node" },
  },
});
