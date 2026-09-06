import { parseArgs } from "node:util";
import { createInterface } from "node:readline/promises";
import { Writable } from "node:stream";
import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { createPasswordHasher } from "./modules/auth/passwords.js";
import { createSelfHostAdmin } from "./modules/self-host/admin.js";

async function main() {
  if (process.env.MISTY_DEPLOYMENT_MODE?.trim().toLowerCase() !== "self_hosted") throw new Error("misty-admin requires MISTY_DEPLOYMENT_MODE=self_hosted");
  const { positionals, values } = parseArgs({ options: { email: { type: "string" } }, allowPositionals: true, strict: true });
  const command = positionals[0];
  if (positionals.length !== 1 || !["bootstrap-token", "reset-password", "disable-account"].includes(command ?? "")) throw new Error("Usage: misty-admin bootstrap-token | reset-password --email EMAIL | disable-account --email EMAIL");
  if (command !== "bootstrap-token" && !values.email?.trim()) throw new Error("--email is required");
  if (command === "bootstrap-token" && values.email !== undefined) throw new Error("bootstrap-token does not accept --email");
  let password = "";
  if (command === "reset-password") {
    process.stderr.write("New password (read from stdin): ");
    const silentOutput = new Writable({ write(_chunk, _encoding, done) { done(); } });
    const input = createInterface({ input: process.stdin, output: silentOutput, terminal: Boolean(process.stdin.isTTY), signal: AbortSignal.timeout(30000) });
    input.once("SIGINT", () => input.close());
    try {
      const line = await input[Symbol.asyncIterator]().next();
      if (line.done) throw new Error("A password is required on stdin");
      password = line.value.trim();
    }
    finally { input.close(); silentOutput.end(); }
    process.stderr.write("\n");
    if (Buffer.byteLength(password, "utf8") < 8 || Buffer.byteLength(password, "utf8") > 72) throw new Error("Password must contain 8 to 72 UTF-8 bytes");
  }
  const pool = createDatabasePool(process.env, "api");
  try {
    const admin = createSelfHostAdmin(pool, await createPasswordHasher());
    if (command === "bootstrap-token") {
      const { token, expiresAt } = await admin.bootstrapToken();
      process.stdout.write(`Bootstrap token (single use, expires ${expiresAt.toISOString()}):\n${token}\n`);
    } else {
      await admin.changeAccount(values.email!, command === "reset-password" ? { kind: "password", password } : { kind: "disable" });
      process.stdout.write(command === "reset-password" ? "Password reset and existing sessions revoked.\n" : "Account disabled and all sessions revoked.\n");
    }
  } finally { await pool.end(); }
}
void main().catch((error: unknown) => {
  // Driver errors may contain private row values. Only CLI validation messages
  // are printable; PostgreSQL diagnostics remain outside terminal output.
  const databaseError = error && typeof error === "object" && "severity" in error;
  process.stderr.write(`${databaseError ? "Database operation failed" : error instanceof Error ? error.message : "Command failed"}\n`);
  process.exitCode = 1;
});
