import { generateText, jsonSchema, Output, transcribe } from "ai";
import { deviceModelId, deviceRoute, type DeviceRole } from "../device-models.js";
import { RoutedModel, speechCall } from "../model-calls.js";
import { ModelSetupError, resolveTranscriptionModel } from "../model-provider.js";
import type { VoiceModels } from "./session.js";

function onDevice(role: DeviceRole, what: string) {
  const model = deviceModelId(role);
  if (!model) throw new ModelSetupError("model_key_needed", `Choose a ${what} model in Settings → Models.`);
  return { model, route: deviceRoute(model) };
}

/** 24 kHz 16-bit mono PCM as a WAV file, which every transcription model takes. */
export function wav(pcm: Buffer, rate = 24_000): Buffer {
  const header = Buffer.alloc(44);
  header.write("RIFF", 0);
  header.writeUInt32LE(36 + pcm.length, 4);
  header.write("WAVEfmt ", 8);
  header.writeUInt32LE(16, 16);
  header.writeUInt16LE(1, 20);
  header.writeUInt16LE(1, 22);
  header.writeUInt32LE(rate, 24);
  header.writeUInt32LE(rate * 2, 28);
  header.writeUInt16LE(2, 32);
  header.writeUInt16LE(16, 34);
  header.write("data", 36);
  header.writeUInt32LE(pcm.length, 40);
  return Buffer.concat([header, pcm]);
}

/** The voice on this device's transcription, fast and speech models. */
export const deviceVoiceModels: VoiceModels = {
  async transcribe(pcm, signal) {
    if (!pcm.length) return "";
    const { model, route } = onDevice("transcription", "transcription");
    const result = await transcribe({ model: await resolveTranscriptionModel(model, route), audio: wav(pcm), maxRetries: 1, abortSignal: signal });
    return result.text;
  },
  async reply(system, messages, tools, signal) {
    const { model, route } = onDevice("fast", "fast");
    const result = await generateText({
      model: new RoutedModel(model, route),
      system,
      messages: messages.length ? messages : [{ role: "user", content: "(The user has not spoken yet.)" }],
      maxOutputTokens: 300,
      maxRetries: 1,
      abortSignal: signal,
      output: Output.object({
        name: "voice_turn",
        schema: jsonSchema<{ say: string; tool: string; instruction: string }>({
          type: "object",
          additionalProperties: false,
          required: ["say", "tool", "instruction"],
          properties: { say: { type: "string" }, tool: { type: "string", enum: ["", ...tools] }, instruction: { type: "string" } },
        }),
      }),
    }).catch(async (error) => {
      // A model that can't give JSON still answers in words.
      if (error instanceof ModelSetupError || signal.aborted) throw error;
      const plain = await generateText({ model: new RoutedModel(model, route), system, messages, maxOutputTokens: 300, maxRetries: 1, abortSignal: signal });
      return { output: { say: plain.text, tool: "", instruction: "" } };
    });
    return result.output;
  },
  async speak(text, previous, next, signal) {
    const { model, route } = onDevice("speech", "speech");
    const spoken = await speechCall({ route, model, text, ...(previous ? { previousText: previous.slice(-4000) } : {}), ...(next ? { nextText: next.slice(0, 4000) } : {}) }, signal);
    return Buffer.from(spoken.audio, "base64");
  },
};
