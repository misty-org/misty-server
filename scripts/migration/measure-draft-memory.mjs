import assert from 'node:assert/strict';
import { createServer, request as httpRequest } from 'node:http';
import { fork, execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { writeFile } from 'node:fs/promises';
import { once } from 'node:events';

const script = fileURLToPath(import.meta.url);
if (!process.env.MISTY_DRAFT_MEMORY_CONTAINER) {
  const root = fileURLToPath(new URL('../../', import.meta.url));
  const image = 'misty-hono-api:migration-test';
  const imageId = execFileSync('docker', ['image', 'inspect', '--format', '{{.Id}}', image], { encoding: 'utf8' }).trim();
  const output = execFileSync('docker', ['run', '--rm', '--network', 'none', '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges',
    '--memory=1g', '--memory-swap=1g', '--tmpfs', '/tmp:rw,noexec,nosuid,size=16m', '-e', 'MISTY_DRAFT_MEMORY_CONTAINER=1',
    '--mount', `type=bind,source=${script},target=/app/draft-memory-probe.mjs,readonly`, image, 'node', '/app/draft-memory-probe.mjs'],
    { cwd: root, encoding: 'utf8', maxBuffer: 1024 * 1024, timeout: 120000 });
  const report = { checkedAt: new Date().toISOString(), imageId, ...JSON.parse(output) };
  await writeFile(new URL('../../docs/migration/mail-draft-memory.json', import.meta.url), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify(report, null, 2));
} else if (process.argv.includes('--api')) {
  const [{ serve }, { createApi }, { prepareDraft }, { createMailDraftWriter }, { pino }] = await Promise.all([
    import('@hono/node-server'), import('/app/dist/apps/api/src/app.js'), import('/app/dist/apps/api/src/modules/mail/providers/draft-input.js'),
    import('/app/dist/apps/api/src/modules/mail/providers/drafts.js'), import('pino'),
  ]);
  const session = { token_hash: 'fixture', user_id: 'fixture-account', app_id: 'inbox', space_id: 'fixture-space', scopes: ['mail.write'], expires_at: new Date(Date.now() + 600000) };
  const runtime = { findSession: async () => session, isSpaceMember: async () => true };
  const auth = { authenticate: async (token) => token === 'account-fixture' ? { id: session.user_id } : null };
  const lease = { connectionId: 'connection_fixture', purpose: 'mailWrite', fingerprint: 'fixture', accessToken: 'fixture-only-token', tokenType: 'Bearer',
    account: { provider: 'google', accountId: session.user_id, display: 'Fixture', status: 'active', errorCode: '' } };
  const service = { createDraft: async (_actor, input, signal) => {
    const prepared = prepareDraft(input);
    const writer = createMailDraftWriter(lease, (operation) => operation(), async () => {}, (_url, options) => fetch(`http://127.0.0.1:${process.env.MISTY_FIXTURE_PROVIDER_PORT}/drafts`, options));
    return { draft: await writer.createDraft(prepared, signal) };
  } };
  const app = createApi({ logger: pino({ level: 'silent' }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    appRuntime: { repository: runtime }, mail: { auth, appRuntime: runtime, service } });
  const server = serve({ fetch: app.fetch, hostname: '127.0.0.1', port: 0 }, (info) => process.send({ ready: info.port, baseline: process.memoryUsage() }));
  process.on('message', async (message) => {
    if (message === 'measure') process.send({ memory: process.memoryUsage(), maxRssKiB: process.resourceUsage().maxRSS });
    if (message === 'close') { server.closeAllConnections(); await new Promise((resolve) => server.close(resolve)); process.disconnect(); }
  });
} else {
  const held = []; let expected = 0, entered;
  const provider = createServer((request, response) => {
    assert.equal(request.method, 'POST'); assert.equal(request.url, '/drafts');
    let bytes = 0; request.on('data', (chunk) => { bytes += chunk.length; });
    request.on('end', () => { assert(bytes > 0 && bytes <= 28 * 1024 * 1024); held.push({ response, bytes }); if (held.length === expected) entered?.(); });
  });
  provider.listen(0, '127.0.0.1'); await once(provider, 'listening');
  const child = fork(script, ['--api'], { env: { ...process.env, MISTY_FIXTURE_PROVIDER_PORT: String(provider.address().port) }, stdio: ['ignore', 'inherit', 'inherit', 'ipc'] });
  const childClosed = once(child, 'exit');
  let ready, measured;
  const readyPromise = new Promise((resolve, reject) => { ready = resolve; child.once('error', reject); child.once('exit', (code) => { if (code) reject(new Error(`API child exited ${code}`)); }); });
  child.on('message', (message) => { if (message.ready) ready(message); else if (message.memory) measured?.(message); });
  const initial = await readyPromise, base = `http://127.0.0.1:${initial.ready}`, results = [];
  const limit = 28 * 1024 * 1024;
  try {
    for (const transport of ['rpc', 'rest']) for (const content of ['binary', 'escaped-text']) {
      const body = { connection_id: 'connection_fixture', to: [{ email: 'fixture@example.invalid' }], subject: 'Memory fixture', text: '' };
      if (content === 'binary') body.attachments = [{ filename: 'fixture.bin', content_type: 'application/octet-stream', data: Buffer.alloc(10 * 1024 * 1024, 37).toString('base64'), inline: false }];
      else body.text = '\u0000'.repeat(Math.floor((limit - Buffer.byteLength(JSON.stringify(body))) / 6));
      const draftBytes = Buffer.byteLength(JSON.stringify(body)); assert(draftBytes <= limit);
      const payload = JSON.stringify(transport === 'rpc' ? { protocol: 2, method: 'mail.drafts.create', params: { body } } : body);
      const path = transport === 'rpc' ? '/v1/app-runtime/rpc' : '/v1/mail/drafts';
      const headers = { Authorization: `Bearer ${transport === 'rpc' ? 'app-fixture' : 'account-fixture'}`, 'Content-Type': 'application/json' };
      expected = 2; const bothHeld = new Promise((resolve) => { entered = resolve; }); const started = performance.now();
      const pending = [0, 1].map(() => fetch(base + path, { method: 'POST', headers, body: payload, signal: AbortSignal.timeout(25000) }));
      await Promise.race([bothHeld, Promise.all(pending).then((responses) => { throw new Error(`Requests did not reach provider: ${responses.map((response) => response.status)}`); })]);
      const overflow = await new Promise((resolve, reject) => {
        const request = httpRequest(base + path, { method: 'POST', headers: { ...headers, 'Content-Length': payload.length, Connection: 'close' } }, (response) => {
          response.resume(); response.once('end', () => { resolve(response.statusCode); request.destroy(); });
        });
        request.on('error', reject); request.setTimeout(2000, () => request.destroy(new Error('Overflow body was awaited'))); request.flushHeaders();
      });
      assert.equal(overflow, 503); assert.equal(held.length, 2);
      const sample = await new Promise((resolve) => { measured = resolve; child.send('measure'); });
      const providerRequestBytes = held.map((entry) => entry.bytes);
      for (const [index, entry] of held.splice(0).entries()) {
        entry.response.writeHead(201, { 'Content-Type': 'application/json' });
        entry.response.end(JSON.stringify({ id: `fixture_${index}`, message: { id: `message_${index}`, threadId: `thread_${index}`, labelIds: ['DRAFT'] } }));
      }
      results.push({ transport, content, concurrency: 2, draftJsonBytes: draftBytes, envelopeBytes: Buffer.byteLength(payload), providerRequestBytes, overflowStatus: overflow,
        heldMemory: sample.memory, maxRssKiB: sample.maxRssKiB, elapsedToHeldMs: Math.round(performance.now() - started) });
      const responses = await Promise.all(pending);
      for (const response of responses) { assert.equal(response.status, 201); assert.equal((await response.json()).draft.provider, 'gmail'); }
    }
    console.log(JSON.stringify({ scope: 'Release-image HTTP/RPC + validation + MIME + loopback provider HTTP; authentication/database are fixtures, not a production load claim', node: process.version,
      containerMemoryLimitBytes: 1024 ** 3, apiBaseline: initial.baseline, results }));
  } finally {
    for (const entry of held.splice(0)) entry.response.destroy();
    if (child.connected) child.send('close'); await childClosed; provider.closeAllConnections(); await new Promise((resolve) => provider.close(resolve));
  }
}
