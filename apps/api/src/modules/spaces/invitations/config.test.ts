import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { invitationToken, loadInvitationConfig } from "./config.js";
it("loads a bounded rotating keyring and preserves the existing invite URL configuration", async () => {
  const directory = await mkdtemp(join(tmpdir(), "misty-invitation-keys-")), file = join(directory, "keys.json");
  try {
    await writeFile(file, JSON.stringify({ active: "new", keys: [{ id: "new", key: Buffer.alloc(32, 1).toString("base64") }, { id: "old", key: Buffer.alloc(32, 2).toString("base64") }] }), { mode: 0o600 });
    const config = (await loadInvitationConfig({ MISTY_INVITATION_TOKEN_KEYS_FILE: file }))!;
    expect(config.baseUrl).toBe("https://mistysys.com/invite");
    expect((await loadInvitationConfig({ MISTY_INVITATION_TOKEN_KEYS_FILE: file, MISTY_INVITATION_URL_BASE: "http://localhost:5173/invite/", MISTY_DEPLOYMENT_MODE: "self_hosted" }))!.baseUrl).toBe("http://localhost:5173/invite");
    const token = invitationToken(config.keys, "old", "invite", "generation", "person@example.invalid");
    expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(invitationToken(config.keys, "old", "invite", "generation", "person@example.invalid")).toBe(token);
    for (const [key, id, generation, email] of [["new", "invite", "generation", "person@example.invalid"], ["old", "different", "generation", "person@example.invalid"], ["old", "invite", "new-generation", "person@example.invalid"], ["old", "invite", "generation", "different@example.invalid"]]) expect(invitationToken(config.keys, key!, id!, generation!, email!)).not.toBe(token);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
it("rejects malformed keys and unsafe link origins without exposing credentials", async () => {
  expect(await loadInvitationConfig({})).toBeNull();
  const directory = await mkdtemp(join(tmpdir(), "misty-invitation-invalid-")), file = join(directory, "keys.json");
  try {
    const entry = { id: "test", key: Buffer.alloc(32, 3).toString("base64") };
    await writeFile(file, JSON.stringify({ active: "test", keys: [entry] }), { mode: 0o600 });
    for (const url of ["http://remote.invalid/invite", "https://user:secret@host.invalid/invite", "https://host.invalid/invite?token=secret", "https://host.invalid/invite#fragment"]) await expect(loadInvitationConfig({ MISTY_INVITATION_TOKEN_KEYS_FILE: file, MISTY_INVITATION_URL_BASE: url })).rejects.toThrow();
    for (const document of [{ active: "missing", keys: [entry] }, { active: "test", keys: [entry, entry] }, { active: "test", keys: [{ ...entry, key: "invalid" }] }]) {
      await writeFile(file, JSON.stringify(document)); await expect(loadInvitationConfig({ MISTY_INVITATION_TOKEN_KEYS_FILE: file })).rejects.toThrow();
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});
