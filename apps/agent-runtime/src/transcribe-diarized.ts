import { transcribe } from "ai";
import { z } from "zod";
import { ModelCallError } from "./model-calls.js";
import { modelRouteSchema, resolveTranscriptionModel } from "./model-provider.js";

const diarizedRequest = z.object({
  route: modelRouteSchema,
  model: z.string().regex(/^soniox\/[\w.-]+$/),
  mediaType: z.string().regex(/^(audio|video)\/[\w.+-]+$/),
  audio: z.string().min(1),
  languages: z.array(z.string().max(16)).max(10).optional(),
  context: z.object({ text: z.string().max(10_000).optional(), terms: z.array(z.string().max(160)).max(200).optional() }).optional(),
});

type Token = { text: string; start_ms?: number; end_ms?: number; speaker?: string | number | null; language?: string | null; translation_status?: string };
export type Turn = { speaker: string; language?: string; start: number; end: number; text: string };

/** Groups Soniox tokens into consecutive speaker turns. */
export function speakerTurns(tokens: Token[]): Turn[] {
  const turns: Turn[] = [];
  for (const token of tokens) {
    if (!token.text || token.translation_status === "translation" || /^<(end|fin)>$/.test(token.text)) continue;
    const speaker = token.speaker == null ? "?" : String(token.speaker);
    const last = turns.at(-1);
    const start = (token.start_ms ?? 0) / 1000;
    const end = (token.end_ms ?? token.start_ms ?? 0) / 1000;
    if (last && last.speaker === speaker) {
      last.text += token.text;
      last.end = Math.max(last.end, end);
      continue;
    }
    turns.push({ speaker, ...(token.language ? { language: token.language } : {}), start, end, text: token.text.trimStart() });
  }
  return turns.map((turn) => ({ ...turn, text: turn.text.trim() })).filter((turn) => turn.text);
}

/** A file or finished capture, transcribed with speaker labels. */
export async function transcribeDiarizedCall(body: unknown, signal?: AbortSignal) {
  const parsed = diarizedRequest.safeParse(body);
  if (!parsed.success) throw new ModelCallError(400, "invalid_model_call", "Invalid model call");
  const request = parsed.data;
  const model = await resolveTranscriptionModel(request.model, request.route);
  const result = await transcribe({
    model,
    audio: Buffer.from(request.audio, "base64"),
    providerOptions: {
      soniox: {
        enableSpeakerDiarization: true,
        enableLanguageIdentification: true,
        ...(request.languages?.length ? { languageHints: request.languages } : {}),
        ...(request.context ? { context: request.context } : {}),
      },
    },
    maxRetries: 1,
    abortSignal: signal,
  });
  const tokens = ((result.providerMetadata?.soniox as { tokens?: Token[] } | undefined)?.tokens ?? []) as Token[];
  return {
    text: result.text,
    language: result.language ?? "",
    durationInSeconds: result.durationInSeconds ?? null,
    turns: speakerTurns(tokens),
  };
}
