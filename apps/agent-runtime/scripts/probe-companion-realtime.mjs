// Explicit diagnostic, never called by the application. Accept only a synthetic
// 24 kHz mono signed-16-bit PCM fixture. No microphone, screenshots, or real tools.
// Run: node scripts/probe-companion-realtime.mjs /absolute/path/to/fixture.pcm
// The fixture should say: "What is the label on my screen?"
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { createGateway } from 'ai';

const model = 'openai/gpt-realtime-2.1';
const pcm = await readFile(process.argv[2]);
if (pcm.length < 4800 || pcm.length > 240000 || pcm.length % 2) {
  throw new Error('Expected a synthetic PCM fixture between 0.1 and 5 seconds');
}
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const report = {
  scope: 'synthetic-provider-probe-only',
  startedAt: new Date().toISOString(),
  model,
  success: false,
  input: { bytes: pcm.length, durationMs: pcm.length / 48, sha256: sha256(pcm) },
  events: {},
  usage: [],
  toolCalls: 0,
  completedResponses: 0,
};
const gateway = createGateway({
  fetch: (url, init) => fetch(url, { ...init, signal: AbortSignal.timeout(15000) }),
});

try {
  const codec = gateway.experimental_realtime(model);
  const secret = await gateway.experimental_realtime.getToken({ model, expiresAfterSeconds: 60 });
  const config = codec.getWebSocketConfig(secret);
  report.tokenMinted = true;
  report.socketHost = new URL(config.url).host;
  await new Promise(resolve => {
    const ws = new WebSocket(config.url, config.protocols);
    const audio = [];
    let audioBytes = 0;
    let finished = false;
    let inputFinal = false;
    let firstResponseDone = false;
    let pendingCall;
    let submitted = false;
    let transcript = '';
    const timer = setTimeout(() => finish('probe_timeout'), 45000);
    function finish(error) {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      if (error) report.error = error;
      report.output = {
        bytes: audioBytes,
        durationMs: audioBytes / 48,
        sha256: sha256(Buffer.concat(audio)),
        expectedLabel: /blue lantern/i.test(transcript),
      };
      report.success = !error && report.manualTurnControl && report.input.expectedQuestion && inputFinal && submitted && audioBytes > 0 && report.output.expectedLabel;
      ws.close();
      resolve();
    }
    function send(event) {
      if (!finished) ws.send(JSON.stringify(codec.serializeClientEvent(event)));
    }
    function submitFixtureResult() {
      // Match the existing capture timing: wait for final input before backend work.
      // This is a fixed synthetic result, not a shortcut around Misty's executor.
      if (!inputFinal || !firstResponseDone || !pendingCall || submitted) return;
      submitted = true;
      send({ type: 'conversation-item-create', item: {
        type: 'function-call-output', callId: pendingCall, name: 'inspect_fixture',
        output: JSON.stringify({ answer: 'Blue lantern.', synthetic: true }),
      } });
      send({ type: 'response-create', options: { modalities: ['audio'] } });
    }
    ws.onopen = () => send({ type: 'session-update', config: {
      instructions: 'This is a synthetic audio transport test. For the user question, call inspect_fixture exactly once. After it returns, speak only its answer verbatim. Do not answer before the function returns.',
      voice: 'alloy',
      outputModalities: ['audio'],
      inputAudioFormat: { type: 'audio/pcm', rate: 24000 },
      outputAudioFormat: { type: 'audio/pcm', rate: 24000 },
      inputAudioTranscription: { model: 'gpt-4o-mini-transcribe', language: 'en' },
      // The pinned OpenAI mapper drops null; explicit disabled serializes to null.
      turnDetection: { type: 'disabled' },
      tools: [{ type: 'function', name: 'inspect_fixture',
        description: 'Read the synthetic screen label. No real screen or action.',
        parameters: { type: 'object', properties: {}, required: [], additionalProperties: false },
      }],
    } });
    ws.onmessage = message => {
      if (finished) return;
      try {
        const event = JSON.parse(message.data);
        report.events[event.type] = (report.events[event.type] ?? 0) + 1;
        if (event.type === 'session-updated') {
          if (report.events[event.type] !== 1) return finish('duplicate_session_update');
          report.manualTurnControl = event.raw?.session?.audio?.input?.turn_detection === null;
          if (!report.manualTurnControl) return finish('manual_turn_control_unconfirmed');
          send({ type: 'input-audio-append', audio: pcm.toString('base64') });
          send({ type: 'input-audio-commit' });
          // The first response can select the fixture tool but cannot speak an
          // unconfirmed result. Speech is requested only after its result returns.
          send({ type: 'response-create', options: { modalities: ['text'] } });
        } else if (event.type === 'input-transcription-completed') {
          inputFinal = true;
          report.input.expectedQuestion = /label/i.test(event.transcript) && /screen/i.test(event.transcript);
          submitFixtureResult();
        } else if (event.type === 'function-call-arguments-done') {
          if (event.name !== 'inspect_fixture' || pendingCall) return finish('unexpected_tool_call');
          if (event.arguments !== '{}') return finish('unexpected_tool_arguments');
          pendingCall = event.callId;
          report.toolCalls += 1;
          submitFixtureResult();
        } else if (event.type === 'audio-delta') {
          if (!submitted) return finish('audio_before_delegation');
          const chunk = Buffer.from(event.delta, 'base64');
          audioBytes += chunk.length;
          if (audioBytes > 3 * 1024 * 1024) return finish('audio_limit');
          audio.push(chunk);
        } else if (event.type === 'audio-transcript-delta') {
          transcript += event.delta;
        } else if (event.type === 'response-done') {
          const usage = event.raw?.response?.usage;
          if (usage) report.usage.push({
            inputTokens: usage.input_tokens, outputTokens: usage.output_tokens,
            inputTextTokens: usage.input_token_details?.text_tokens,
            inputAudioTokens: usage.input_token_details?.audio_tokens,
            cachedInputTokens: usage.input_token_details?.cached_tokens,
            outputTextTokens: usage.output_token_details?.text_tokens,
            outputAudioTokens: usage.output_token_details?.audio_tokens,
          });
          if (event.status !== 'completed') return finish('response_incomplete');
          report.completedResponses += 1;
          if (submitted) return finish();
          firstResponseDone = true;
          submitFixtureResult();
        } else if (event.type === 'error') {
          report.providerErrorCode = typeof event.code === 'string' && /^[a-z0-9_-]{1,80}$/i.test(event.code) ? event.code : 'redacted';
          finish('provider_error');
        }
      } catch {
        finish('invalid_event');
      }
    };
    ws.onerror = () => finish('websocket_error');
    ws.onclose = () => finish('unexpected_close');
  });
} catch (error) {
  report.error = 'token_or_connection_failed';
  report.httpStatus = error.statusCode ?? error.cause?.statusCode ?? null;
}
// Never print tokens, raw events, transcripts, audio bytes, or exception bodies.
console.log(JSON.stringify(report, null, 2));
process.exitCode = report.success ? 0 : 1;
