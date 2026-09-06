import { execFileSync } from "node:child_process";
import { generateKeyPairSync, randomUUID, randomBytes } from "node:crypto";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout } from "node:timers/promises";

const root = new URL("../../", import.meta.url);
const temporary = await mkdtemp(join(tmpdir(), "misty-image-smoke-"));
const containers = [];
const results = [];
const docker = (args) => execFileSync("docker", args, { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
try {
  const privateKey = generateKeyPairSync("ed25519").privateKey.export({ type: "pkcs8", format: "pem" });
  const keyFile = join(temporary, "test-only-key.pem");
  await writeFile(keyFile, privateKey, { mode: 0o444 });
  const apiPublicKey = generateKeyPairSync("ed25519").publicKey.export({ type: "spki", format: "pem" });
  const apiKeysFile = join(temporary, "api-public-keys.json");
  await writeFile(apiKeysFile, JSON.stringify([{ id: "api-smoke-only", publicKey: apiPublicKey }]), { mode: 0o444 });
  const recoveryKeysFile = join(temporary, "recovery-keys.json");
  await writeFile(recoveryKeysFile, JSON.stringify({ active: "smoke-only", keys: [{ id: "smoke-only", key: randomBytes(32).toString("base64") }] }), { mode: 0o444 });
  const invitationKeysFile = join(temporary, "invitation-keys.json");
  await writeFile(invitationKeysFile, JSON.stringify({ active: "invitation-smoke", keys: [{ id: "invitation-smoke", key: randomBytes(32).toString("base64") }] }), { mode: 0o444 });
  await chmod(temporary, 0o755);
  const collaborationKey = generateKeyPairSync("ed25519").privateKey.export({ type: "pkcs8", format: "der" }).toString("base64");
  const built = new Set();
  for (const [service, deployment, storage, paymentsMode] of [["api", "hosted"], ["api", "self_hosted"], ["api", "self_hosted", "filesystem"], ["payments", "hosted", null, "paused"], ["payments", "hosted", null, "delivery"], ["payments", "hosted", null, "active"]]) {
    const image = service === "api" ? "misty-hono-api:migration-test" : "misty-payments:migration-test";
    if (!process.argv.includes("--skip-build") && !built.has(image)) docker(["build", "-f", `apps/${service}/Dockerfile`, "-t", image, "."]);
    built.add(image);
    const name = `misty-${service}-smoke-${randomUUID().slice(0, 8)}`;
    const port = service === "api" ? 8082 : 8083;
    const prefix = service === "api" ? "DB_" : "BILLING_DB_";
    const environment = {
      [`${prefix}HOST`]: "127.0.0.1", [`${prefix}PORT`]: "9", [`${prefix}NAME`]: "misty_smoke_test",
      [`${prefix}USER`]: "smoke_only", [`${prefix}PASSWORD`]: "smoke_only", [`${prefix}SSLMODE`]: "disable",
      LOG_LEVEL: "silent", MISTY_DEPLOYMENT_MODE: deployment,
      ...(storage === "filesystem" ? { MISTY_LIBRARY_BACKEND: "filesystem", MISTY_LIBRARY_FILESYSTEM_DIR: "/tmp/avatar-smoke" } : {}),
      ...(service === "api" ? { SPACE_LINK_ENCRYPTION_KEY: randomBytes(32).toString("base64"), JOURNAL_COLLAB_TICKET_PRIVATE_KEY: collaborationKey,
        JOURNAL_COLLAB_ROOM_SALT: randomBytes(32).toString("base64"), JOURNAL_COLLAB_CONTROL_SECRET: randomBytes(32).toString("base64"),
        JOURNAL_COLLAB_PROJECTION_SECRET: randomBytes(32).toString("base64") } : {}),
      ...(service === "api" && deployment === "hosted" ? { PAYMENTS_COMMANDS_URL: "https://payments.example.invalid/internal/billing", PAYMENTS_SUMMARY_URL: "https://payments.example.invalid/internal/billing/summary", API_SIGNING_KEY_FILE: "/run/test-key.pem", API_SIGNING_KEY_ID: "smoke-only", MAILJET_API_KEY: "smoke-only-key", MAILJET_SECRET_KEY: "smoke-only-secret", MAILJET_FROM_EMAIL: "smoke@example.invalid", AUTH_RECOVERY_TOKEN_KEYS_FILE: "/run/recovery-keys.json", MISTY_INVITATION_TOKEN_KEYS_FILE: "/run/invitation-keys.json" } : {}),
      ...(service === "payments" ? {
        PAYMENTS_MODE: paymentsMode,
        STRIPE_SECRET_KEY: "sk_test_smoke_not_a_real_key", STRIPE_WEBHOOK_SECRET: "whsec_smoke_not_a_real_secret",
        STRIPE_PRICE_PRO_MONTHLY: "price_pro_month", STRIPE_PRICE_PRO_YEARLY: "price_pro_year",
        STRIPE_PRICE_MAX_MONTHLY: "price_max_month", STRIPE_PRICE_MAX_YEARLY: "price_max_year",
        PAYMENTS_SIGNING_KEY_FILE: "/run/test-key.pem", PAYMENTS_SIGNING_KEY_ID: "smoke-only",
        API_ENTITLEMENTS_URL: "https://api.example.invalid/internal/payments/entitlements",
        API_VERIFICATION_KEYS_FILE: "/run/api-public-keys.json",
        STRIPE_CHECKOUT_SUCCESS_URL: "https://website.example.invalid/success",
        STRIPE_CHECKOUT_CANCEL_URL: "https://website.example.invalid/cancel",
        STRIPE_PORTAL_RETURN_URL: "https://website.example.invalid/billing",
      } : {}),
    };
    docker(["run", "-d", "--name", name, "--network", "none", "--read-only", "--cap-drop=ALL",
      "--security-opt=no-new-privileges", "--tmpfs", `/tmp:rw,noexec,nosuid,size=${storage === "filesystem" ? "128m" : "16m"}`,
      "--mount", `type=bind,source=${keyFile},target=/run/test-key.pem,readonly`,
      "--mount", `type=bind,source=${apiKeysFile},target=/run/api-public-keys.json,readonly`,
      "--mount", `type=bind,source=${recoveryKeysFile},target=/run/recovery-keys.json,readonly`,
      "--mount", `type=bind,source=${invitationKeysFile},target=/run/invitation-keys.json,readonly`,
      ...Object.entries(environment).flatMap(([key, value]) => ["-e", `${key}=${value}`]), image]);
    containers.push(name);
    const probe = `
      import assert from 'node:assert/strict';
      import {existsSync} from 'node:fs';
      const base='http://127.0.0.1:${port}';
      const live=await fetch(base+'/livez',{signal:AbortSignal.timeout(2000)});
      assert.equal(live.status,200);
      const ready=await fetch(base+'/readyz',{signal:AbortSignal.timeout(8000)});
      assert.equal(ready.status,503);
      assert.equal(process.getuid(),1000);
      let stripeInstalled=false;
      try { await import('stripe'); stripeInstalled=true; } catch(e) { if(e.code!=='ERR_MODULE_NOT_FOUND') throw e; }
      assert.equal(stripeInstalled,${service === "payments"});
      let s3Installed=false;
      try { await import('@aws-sdk/client-s3'); s3Installed=true; } catch(e) { if(e.code!=='ERR_MODULE_NOT_FOUND') throw e; }
      assert.equal(s3Installed,${service === "api"});
      let mimeComposerInstalled=false;
      try { await import('nodemailer/lib/mail-composer/index.js'); mimeComposerInstalled=true; } catch(e) { if(e.code!=='ERR_MODULE_NOT_FOUND') throw e; }
      assert.equal(mimeComposerInstalled,${service === "api"});
      assert.equal(existsSync('/app/dist/apps/${service === "api" ? "payments" : "api"}'),false);
      if (${storage === "filesystem"}) {
        const {createFilesystemByteStore}=await import('./dist/apps/api/src/modules/storage/filesystem-store.js');
        const {createHash}=await import('node:crypto');
        const store=await createFilesystemByteStore('/tmp/avatar-smoke');
        const data=Buffer.from('private Linux filesystem avatar proof');
        await store.putBytes('avatars/avatar_12345678',data,{byteSize:data.length,sha256:createHash('sha256').update(data).digest('hex'),mimeType:'image/png'});
        assert.deepEqual(await store.getBytes('avatars/avatar_12345678',100),data);
        await store.delete('avatars/avatar_12345678');
        assert.equal(await store.getBytes('avatars/avatar_12345678',100),null);
      }
      if (${deployment === "self_hosted"}) {
        const recovery=await fetch(base+'/auth/forgot',{method:'POST',body:'{}'});
        assert.equal(recovery.status,501);
      }
      if (${service === "api"}) {
        const {createExportFile}=await import('./dist/apps/api/src/modules/accounts/export-file.js');
        let exportClosed=false;
        const exported=await createExportFile({signal:new AbortController().signal,onClose:()=>{exportClosed=true;}});
        await exported.write('{"portable":"private export"}');
        assert.deepEqual(await exported.response().json(),{portable:'private export'});
        assert.equal(exportClosed,true);
        assert.equal((await fetch(base+'/me/export',{method:'POST',body:'{}'})).status,401);
        // Initiation must stay unmounted until all deletion cleanup handlers exist.
        for (const path of ['/me/deletion','/account/deletion/status']) assert.equal((await fetch(base+path,{method:'POST',body:'{}'})).status,404);
        assert.equal((await fetch(base+'/billing/usage')).status,${deployment === "self_hosted" ? 501 : 401});
        for (const path of ['/billing/checkout-session','/billing/portal-session','/billing/trial/start','/billing/credit-checkout-session']) {
          const billing=await fetch(base+path,{method:'POST',body:'{}'});
          assert.equal(billing.status,${deployment === "self_hosted" ? 501 : 401});
        }
      }
      if (${service === "payments"}) {
        assert.equal((await fetch(base+'/internal/billing/account-closure',{method:'POST',body:'{}'})).status,${paymentsMode === "active" ? 401 : 404});
        const webhook=await fetch(base+'/stripe/webhook',{method:'POST',body:'{}'});
        assert.equal(webhook.status,400);
        for (const path of ['/internal/billing/checkout','/internal/billing/portal']) {
          const command=await fetch(base+path,{method:'POST',body:'{}'});
          assert.equal(command.status,${paymentsMode === "active" ? 401 : 404});
        }
      }
      console.log(JSON.stringify({service:'${service}',deployment:'${deployment}',storageBackend:${JSON.stringify(storage ?? null)},paymentsMode:${JSON.stringify(paymentsMode ?? null)},node:process.version,uid:process.getuid(),liveness:live.status,readiness:ready.status,stripeInstalled}));
    `;
    let result;
    for (let attempt = 0; attempt < 20; attempt++) {
      try { result = JSON.parse(docker(["exec", name, "node", "--input-type=module", "-e", probe])); break; }
      catch (error) { if (attempt === 19) throw error; await setTimeout(250); }
    }
    docker(["stop", "--time", "15", name]);
    const state = JSON.parse(docker(["inspect", "--format", "{{json .State}}", name]));
    if (state.ExitCode !== 0) throw new Error(`${service} failed graceful shutdown: ${state.ExitCode}`);
    results.push({ ...result, imageId: docker(["image", "inspect", "--format", "{{.Id}}", image]), shutdownExitCode: state.ExitCode });
  }
  const report = { checkedAt: new Date().toISOString(), scope: "Local isolated image smoke; no production or Stripe network access", results };
  await writeFile(new URL("docs/migration/image-smoke.json", root), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify(report, null, 2));
} finally {
  for (const name of containers) { try { docker(["rm", "-f", name]); } catch {} }
  await rm(temporary, { recursive: true, force: true });
}
