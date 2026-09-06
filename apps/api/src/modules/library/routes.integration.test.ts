import { createHash, randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createSpaceRepository } from "../spaces/repository.js";
import { createLibraryRepository } from "./repository.js";
import { createLibraryReauthentication } from "./reauthentication.js";
import { createLibraryDownloads } from "./downloads.js";
import { createLibraryDownloadRepository } from "./download-repository.js";
import { createLibraryMutations } from "./mutations.js";
import { createLibraryOrganization } from "./organization.js";
import { createEgressGuard } from "../storage/egress.js";
import { createFilesystemByteStore } from "../storage/filesystem-store.js";
import { createS3Store } from "../storage/s3-store.js";

const admin = createTestDatabase(), users: string[] = [];
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_library_test') THEN CREATE ROLE misty_library_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_library_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,owner_storage_usage,
      space_setup_integrations,space_creation_requests,space_events,library_reauthentication_grants TO misty_library_test;
    GRANT SELECT ON space_member_permission_overrides,space_invitations,space_conversations,space_conversation_members,
      library_files,library_blobs,library_item_versions,space_library_items,space_library_asset_stack_members,space_library_asset_stacks,library_derivatives,
      space_album_items,space_albums,space_library_item_views,space_library_grants,space_message_library_references,space_message_attachments,
      space_library_imports,space_storage_contributions,space_upload_reservations,space_rendition_reservations TO misty_library_test;
    GRANT SELECT,INSERT ON space_library_audit_events TO misty_library_test;
    GRANT SELECT,INSERT,UPDATE ON space_library_item_views TO misty_library_test;
    GRANT UPDATE ON space_library_items,space_storage_contributions,space_albums TO misty_library_test;
    GRANT INSERT,DELETE ON space_album_items TO misty_library_test;
    GRANT UPDATE ON space_album_items TO misty_library_test;
    GRANT INSERT,DELETE ON space_albums TO misty_library_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON space_album_folders TO misty_library_test;
    GRANT SELECT,INSERT ON space_library_groups TO misty_library_test;
    GRANT USAGE,SELECT ON space_events_id_seq,space_library_audit_events_id_seq TO misty_library_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_library_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async tx => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map(row => row.security_domain_id);
    const blobs = (await tx.query<{ blob_id: string }>("DELETE FROM library_files WHERE uploader_user_id=ANY($1::text[]) RETURNING blob_id", [users])).rows.map(row => row.blob_id);
    await tx.query("DELETE FROM library_blobs WHERE id=ANY($1::text[])", [blobs]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture(store: Parameters<typeof createLibraryDownloads>[0]["store"] = null, egress = createEgressGuard({ perIdentity: 1000000n, global: 2000000n }), now = () => new Date()) {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    library: { auth, repository: createLibraryRepository(application), reauthenticate: createLibraryReauthentication(application, passwords), mutations: createLibraryMutations(application), organization: createLibraryOrganization(application),
      downloads: createLibraryDownloads({ repository: createLibraryDownloadRepository(application), store, egress, downloadTtlMs: 120000, now }) } });
  const account = async () => {
    const username = `library_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const value = await auth.register({ username, email: `${username}@example.invalid`, name: "Library test", password: "test-password", analyticsEnabled: false }); users.push(value.user.id); return value;
  };
  const owner = await account(), member = await account(), outsider = await account();
  const { space } = await createSpaceRepository(application).create(owner.user.id, { name: "Library", template_id: "blank", integration_providers: [] }, "");
  await admin.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')", [space.id, member.user.id]);
  const root = `/spaces/${space.id}/library`;
  const request = (path = root, token = owner.token, grant = "", body?: unknown, method = body === undefined ? "GET" : "POST") => app.request(path, { method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "X-Misty-Library-Reauthentication": grant }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  const item = async (name: string, options: { hidden?: boolean; trash?: boolean; conversation?: string; favorite?: boolean; tags?: string[]; mime?: string } = {}) => {
    const blob = randomUUID(), file = randomUUID(), id = randomUUID();
    const domain = (await admin.query("SELECT security_domain_id FROM spaces WHERE id=$1", [space.id])).rows[0].security_domain_id;
    await admin.query("INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,server_detected_mime_type,lifecycle_state) VALUES($1,$2,$3,$4,128,$5,'ready')", [blob, domain, `library/${blob}`, blob.replaceAll("-", "").repeat(2), options.mime ?? "image/jpeg"]);
    await admin.query("INSERT INTO library_files(id,blob_id,security_domain_id,uploader_user_id,original_filename,lifecycle_state,original_uploaded_at) VALUES($1,$2,$3,$4,$5,'ready','2026-09-05T12:00:00Z')", [file, blob, domain, owner.user.id, `${name}.jpg`]);
    await admin.query(`INSERT INTO space_library_items(id,space_id,file_id,contributing_user_id,display_name,added_by_user_id,hidden,lifecycle_state,audience_kind,audience_conversation_id,favorite,tags)
      VALUES($1,$2,$3,$4,$5,$4,$6,$7,$8,$9,$10,$11::jsonb)`, [id, space.id, file, owner.user.id, name, options.hidden ?? false, options.trash ? "trash" : "ready", options.conversation ? "conversation" : "space", options.conversation ?? null, options.favorite ?? false, JSON.stringify(options.tags ?? [])]);
    return id;
  };
  return { owner, member, outsider, spaceId: space.id, root, request, item };
}

it("lists and retrieves legacy Library DTOs with structured search, filters, aliases and pagination", async () => {
  const f = await fixture(), first = await f.item("Alpha", { favorite: true, tags: ["Trip"] }), second = await f.item("Bravo"), document = await f.item("Notes", { mime: "application/pdf" });
  const response = await f.request(`/api${f.root}?sort=name&direction=asc&limit=1`);
  expect(response.status, await response.clone().text()).toBe(200); const page = await response.json();
  expect(page.items).toHaveLength(1); expect(page.next_after).toBe(first);
  expect(page.items[0]).toMatchObject({ id: first, display_name: "Alpha", version: 1, location_override: null, file: { original_filename: "Alpha.jpg", version: 1 } });
  expect(page.items[0]).not.toHaveProperty("trashed_at"); expect(JSON.stringify(page)).not.toContain("r2_object_key");
  const next = await (await f.request(`${f.root}?sort=name&direction=asc&limit=1&after=${first}`)).json(); expect(next.items[0].id).toBe(second);
  const query = encodeURIComponent('tag:trip type:photos favorite:true year:2026 Alpha');
  expect((await (await f.request(`${f.root}?q=${query}`)).json()).items.map((item: { id: string }) => item.id)).toEqual([first]);
  expect((await (await f.request(`${f.root}?utility=documents`)).json()).items.map((item: { id: string }) => item.id)).toEqual([document]);
  expect((await (await f.request(`/v1${f.root}/items/${first}`)).json()).id).toBe(first);
  expect((await f.request(`${f.root}?q=type:invalid`)).status).toBe(400);
  expect((await f.request(`${f.root}/items/missing`)).status).toBe(404);
});

it("filters private conversation items from lists, details and facets and enforces Space permissions", async () => {
  const f = await fixture(), shared = await f.item("Shared", { tags: ["public"] }), conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,created_by_user_id,title) VALUES($1,$2,$3,'Private')", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  const privateItem = await f.item("Secret", { conversation, tags: ["secret"] }), hidden = await f.item("Hidden", { hidden: true });
  const album = randomUUID(); await admin.query("INSERT INTO space_albums(id,space_id,name,created_by_user_id) VALUES($1,$2,'Album',$3)", [album, f.spaceId, f.owner.user.id]);
  for (const id of [shared, privateItem, hidden]) await admin.query("INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id) VALUES($1,$2,$3)", [album, id, f.owner.user.id]);
  expect((await (await f.request(f.root, f.member.token)).json()).items.map((item: { id: string }) => item.id)).toEqual([shared]);
  expect((await f.request(`${f.root}/items/${privateItem}`, f.member.token)).status).toBe(404);
  expect((await f.request(`${f.root}/items/${privateItem}`)).status).toBe(200);
  const facets = await f.request(`${f.root}/facets`, f.member.token); expect(facets.status, await facets.clone().text()).toBe(200);
  expect(await facets.json()).toMatchObject({ total: 1, hidden: 1, tags: [{ value: "public", label: "public", count: 1 }], albums: [{ value: album, label: "Album", count: 1 }] });
  expect((await f.request(f.root, f.outsider.token)).status).toBe(403);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'library.view','deny',$2)", [f.spaceId, f.member.user.id]);
  expect((await f.request(f.root, f.member.token)).status).toBe(403);
});

it("requires password-backed grants for effective hidden searches and trash, with scope, user and expiry checks", async () => {
  const f = await fixture(), hidden = await f.item("Hidden", { hidden: true }), trash = await f.item("Trash", { trash: true });
  const reauth = (scope: string, password = "test-password") => f.request(`${f.root}/reauthenticate`, f.owner.token, "", { scope, password });
  expect((await f.request(`${f.root}?q=hidden:true`)).status).toBe(401);
  expect((await f.request(`${f.root}?visibility=all`)).status).toBe(401);
  expect((await reauth("hidden", "wrong-password")).status).toBe(401);
  const response = await reauth("hidden"); expect(response.status, await response.clone().text()).toBe(200); const grant = await response.json();
  expect((await (await f.request(`${f.root}?q=hidden:true`, f.owner.token, grant.token)).json()).items[0].id).toBe(hidden);
  expect((await f.request(`${f.root}/items/${hidden}`, f.member.token, grant.token)).status).toBe(401);
  expect((await f.request(`${f.root}/items/${trash}`, f.owner.token, grant.token)).status).toBe(401);
  const deletedGrant = await (await reauth("recently_deleted")).json();
  expect((await (await f.request(`${f.root}?collection=recently-deleted`, f.owner.token, deletedGrant.token)).json()).items[0].id).toBe(trash);
  const stored = (await admin.query("SELECT token_hash FROM library_reauthentication_grants WHERE user_id=$1 AND scope='hidden'", [f.owner.user.id])).rows[0]; expect(stored.token_hash).not.toBe(grant.token);
  await admin.query("UPDATE library_reauthentication_grants SET expires_at=now()-interval '1 second' WHERE user_id=$1", [f.owner.user.id]);
  expect((await f.request(`${f.root}/items/${hidden}`, f.owner.token, grant.token)).status).toBe(401);
  expect((await admin.query("SELECT outcome FROM space_library_audit_events WHERE space_id=$1 ORDER BY id", [f.spaceId])).rows.map(row => row.outcome)).toEqual(["denied", "success", "success"]);
});

it("returns owner storage balances and only availability to members", async () => {
  const f = await fixture();
  await admin.query("UPDATE space_storage_usage SET used_bytes=999 WHERE space_id=$1", [f.spaceId]);
  const usage = await f.request(`${f.root}/usage`); expect(usage.status, await usage.clone().text()).toBe(200);
  expect(await usage.json()).toMatchObject({ space_id: f.spaceId, owner_user_id: f.owner.user.id, space_used_bytes: 0, personal_used_bytes: 0 });
  expect(await (await f.request(`${f.root}/usage`, f.member.token)).json()).toEqual({ space_id: f.spaceId, storage_available: true });
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'storage.view_own_usage','deny',$2)", [f.spaceId, f.member.user.id]);
  expect((await f.request(`${f.root}/usage`, f.member.token)).status).toBe(403);
});

it("returns signed original/current descriptors, records views and rejects unauthorized or over-budget downloads", async () => {
  const now = new Date("2026-09-05T12:00:00Z");
  const store = createS3Store({ endpoint: "https://r2.example.invalid", region: "auto", bucket: "library", accessKeyId: "test", secretAccessKey: "test", forcePathStyle: true }, () => now);
  try {
    const f = await fixture(store, createEgressGuard({ perIdentity: 256n, global: 512n }), () => now), id = await f.item("Photo"), path = `${f.root}/items/${id}/download`;
    const response = await f.request(`/api${path}`); expect(response.status, await response.clone().text()).toBe(200);
    expect(response.headers.get("X-Misty-Signed-Download")).toBe("1"); expect(response.headers.get("Cache-Control")).toBe("private, no-store");
    const descriptor = await response.json(); expect(descriptor.filename).toBe("Photo"); expect(new URL(descriptor.url).searchParams.get("X-Amz-Expires")).toBe("120");
    const original = await (await f.request(`/v1${path}?version=original`)).json(); expect(original.filename).toBe("Photo.jpg");
    expect((await admin.query("SELECT view_count FROM space_library_item_views WHERE space_library_item_id=$1", [id])).rows[0].view_count).toBe("2");
    const limited = await f.request(path); expect(limited.status).toBe(429); expect(limited.headers.get("Retry-After")).toBe("3600");
    expect((await f.request(path, f.outsider.token)).status).toBe(403);
    const hidden = await f.item("Hidden", { hidden: true }); expect((await f.request(`${f.root}/items/${hidden}/download`, f.member.token)).status).toBe(401);
    await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'library.download','deny',$2)", [f.spaceId, f.member.user.id]);
    expect((await f.request(path, f.member.token)).status).toBe(403);
  } finally { store.close(); }
});

it("streams local objects with safe headers and refuses mismatched or missing storage objects", async () => {
  const directory = await mkdtemp(join(tmpdir(), "misty-library-download-")), store = await createFilesystemByteStore(directory);
  try {
    const f = await fixture(store), id = await f.item("../résumé\r\n.json", { mime: "application/json" });
    const source = (await admin.query("SELECT b.id,b.r2_object_key FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=$1", [id])).rows[0];
    const bytes = Buffer.from('{"file":true}'), metadata = { byteSize: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex"), mimeType: "application/json" };
    await admin.query("UPDATE library_blobs SET byte_size=$2,sha256=$3 WHERE id=$1", [source.id, metadata.byteSize, metadata.sha256]);
    await store.putBytes(source.r2_object_key, bytes, metadata);
    const path = `${f.root}/items/${id}/download`, response = await f.request(path);
    expect(response.status, await response.clone().text()).toBe(200); expect(response.headers.get("X-Misty-Signed-Download")).toBeNull();
    expect(response.headers.get("Content-Type")).toBe("application/json"); expect(response.headers.get("Content-Length")).toBe(String(bytes.length));
    expect(response.headers.get("Content-Disposition")).toContain("filename*=UTF-8''r%C3%A9sum%C3%A9.json");
    expect(Buffer.from(await response.arrayBuffer())).toEqual(bytes);
    await admin.query("UPDATE library_blobs SET sha256=$2 WHERE id=$1", [source.id, "f".repeat(64)]);
    const mismatch = await f.request(path); expect(mismatch.status).toBe(409); expect(await mismatch.json()).toEqual({ code: "library_object_mismatch" });
    await store.delete(source.r2_object_key); expect((await f.request(path)).status).toBe(404);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

it("selects ready edited renditions, keeps original downloads unchanged and falls back while processing", async () => {
  const f = await fixture({ signDownload: async (key, filename, expires) => ({ url: `https://r2.example.invalid/${key}`, filename, expires_at: expires.toISOString() }) });
  const id = await f.item("Photo.jpg"), rendered = await f.item("Rendered", { mime: "video/mp4" }), version = randomUUID();
  const blob = (await admin.query("SELECT f.blob_id FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1", [rendered])).rows[0].blob_id;
  await admin.query("INSERT INTO library_item_versions(id,space_library_item_id,created_by_user_id,rendition_blob_id,version_number,rendition_state) VALUES($1,$2,$3,$4,1,'ready')", [version, id, f.owner.user.id, blob]);
  await admin.query("UPDATE space_library_items SET current_edit_version_id=$2 WHERE id=$1", [id, version]);
  const path = `${f.root}/items/${id}/download`, edited = await (await f.request(path)).json();
  expect(edited).toMatchObject({ filename: "Photo-edited.mp4", url: `https://r2.example.invalid/library/${blob}` });
  const original = await (await f.request(`${path}?version=original`)).json(); expect(original.filename).toBe("Photo.jpg.jpg"); expect(original.url).not.toBe(edited.url);
  await admin.query("UPDATE library_item_versions SET rendition_state='processing' WHERE id=$1", [version]);
  const fallback = await (await f.request(path)).json(); expect(fallback.filename).toBe("Photo.jpg"); expect(fallback.url).toBe(original.url);
});

it("updates metadata with legacy normalization, version conflicts and edit permissions", async () => {
  const f = await fixture(), id = await f.item("Original"), path = `${f.root}/items/${id}`;
  const value = { version: 1, display_name: "  Renamed  ", caption: " Caption ", tags: [" Trip ", "trip", "", "x".repeat(81)], favorite: true, hidden: false };
  const updated = await f.request(`/api${path}`, f.owner.token, "", value, "PATCH"); expect(updated.status, await updated.clone().text()).toBe(200);
  expect(await updated.json()).toMatchObject({ id, display_name: "Renamed", caption: "Caption", tags: ["Trip"], favorite: true, hidden: false, version: 2 });
  const conflict = await f.request(path, f.owner.token, "", value, "PATCH"); expect(conflict.status).toBe(409); expect(await conflict.json()).toEqual({ code: "version_conflict" });
  expect((await f.request(path, f.owner.token, "", { version: 2, display_name: "" }, "PATCH")).status).toBe(400);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'library.edit','deny',$2)", [f.spaceId, f.member.user.id]);
  expect((await f.request(path, f.member.token, "", { ...value, version: 2 }, "PATCH")).status).toBe(403);
  const reset = await f.request(`/v1${path}`, f.owner.token, "", { version: 2, display_name: "Defaults", tags: null, caption: null }, "PATCH");
  expect(reset.status).toBe(200); expect(await reset.json()).toMatchObject({ caption: "", tags: [], favorite: false, version: 3 });
});

it("applies bulk flags, tags, dates, locations and album membership in requested item order", async () => {
  const f = await fixture(), ids = [await f.item("One"), await f.item("Two")];
  let versions = ids.map(id => ({ id, version: 1 }));
  const grant = await (await f.request(`${f.root}/reauthenticate`, f.owner.token, "", { password: "test-password", scope: "hidden" })).json();
  const action = async (name: string, extra: Record<string, unknown> = {}) => {
    const response = await f.request(`/v1${f.root}/items/bulk`, f.owner.token, grant.token, { action: name, items: versions, ...extra });
    expect(response.status, await response.clone().text()).toBe(200); const { items } = await response.json();
    expect(items.map((item: { id: string }) => item.id)).toEqual(ids);
    versions = items.map((item: { id: string; version: number }) => ({ id: item.id, version: item.version })); return items;
  };
  expect((await action("favorite"))[0].favorite).toBe(true); expect((await action("unfavorite"))[0].favorite).toBe(false);
  expect((await action("hide"))[0].hidden).toBe(true);
  expect((await f.request(`${f.root}/items/bulk`, f.owner.token, "", { action: "unhide", items: versions })).status).toBe(401);
  expect((await action("unhide"))[0].hidden).toBe(false);
  expect((await action("add_tags", { tags: [" Trip ", "trip", "Food"] }))[0].tags).toEqual(["Food", "Trip"]);
  expect((await action("remove_tags", { tags: ["TRIP"] }))[0].tags).toEqual(["Food"]);
  expect((await action("set_date", { date_override: "2026-09-01T12:00:00Z" }))[0].date_override).toBe("2026-09-01T12:00:00.000Z");
  expect((await action("clear_date"))[0]).not.toHaveProperty("date_override");
  expect((await action("set_location", { location_override: { latitude: 12, longitude: 34 } }))[0].location_override).toEqual({ latitude: 12, longitude: 34 });
  expect((await action("clear_location"))[0].location_override).toBeNull();
  const album = randomUUID(); await admin.query("INSERT INTO space_albums(id,space_id,name,created_by_user_id) VALUES($1,$2,'Album',$3)", [album, f.spaceId, f.owner.user.id]);
  const before = structuredClone(versions); await action("add_to_album", { album_id: album }); expect(versions).toEqual(before);
  expect((await admin.query("SELECT space_library_item_id FROM space_album_items WHERE album_id=$1", [album])).rowCount).toBe(2);
  await action("remove_from_album", { album_id: album }); expect(versions).toEqual(before);
  expect((await admin.query("SELECT version FROM space_albums WHERE id=$1", [album])).rows[0].version).toBe("3");
  expect((await admin.query("SELECT * FROM space_album_items WHERE album_id=$1", [album])).rowCount).toBe(0);
});

it("trashes and restores items and bulk contributions atomically without releasing recovery storage", async () => {
  const f = await fixture(), ids = [await f.item("One"), await f.item("Two")];
  for (const id of ids) await admin.query("INSERT INTO space_storage_contributions(id,space_id,user_id,file_id,source_kind,source_id,logical_bytes,state) SELECT $1,space_id,$2,file_id,'library_item',id,128,'active' FROM space_library_items WHERE id=$3", [randomUUID(), f.owner.user.id, id]);
  const trashed = await f.request(`/api${f.root}/items/${ids[0]}/trash`, f.owner.token, "", {}); expect(trashed.status).toBe(200);
  expect(await trashed.json()).toMatchObject({ lifecycle_state: "trash", version: 2 });
  expect((await admin.query("SELECT state FROM space_storage_contributions WHERE source_id=$1", [ids[0]])).rows[0].state).toBe("recovery");
  expect((await (await f.request(`${f.root}/usage`)).json()).space_used_bytes).toBe(256);
  expect((await f.request(`${f.root}/items/${ids[0]}/restore`, f.owner.token, "", {})).status).toBe(401);
  const grant = await (await f.request(`${f.root}/reauthenticate`, f.owner.token, "", { password: "test-password", scope: "recently_deleted" })).json();
  const restored = await f.request(`/v1${f.root}/items/${ids[0]}/restore`, f.owner.token, grant.token, {}); expect(restored.status).toBe(200);
  expect(await restored.json()).toMatchObject({ lifecycle_state: "ready", version: 3 });
  const bulk = await f.request(`${f.root}/items/bulk`, f.owner.token, "", { action: "trash", items: [{ id: ids[0], version: 3 }, { id: ids[1], version: 1 }] }); expect(bulk.status).toBe(200);
  const items = (await bulk.json()).items.map((item: { id: string; version: number }) => ({ id: item.id, version: item.version }));
  await admin.query("UPDATE space_library_items SET recover_until=now()-interval '1 second' WHERE id=$1", [ids[1]]);
  expect((await f.request(`${f.root}/items/bulk`, f.owner.token, grant.token, { action: "restore", items })).status).toBe(404);
  expect((await admin.query("SELECT state FROM space_storage_contributions WHERE source_id=ANY($1::text[])", [ids])).rows.map(row => row.state)).toEqual(["recovery", "recovery"]);
  await admin.query("UPDATE space_library_items SET recover_until=now()+interval '1 day' WHERE id=$1", [ids[1]]);
  const result = await f.request(`${f.root}/items/bulk`, f.owner.token, grant.token, { action: "restore", items }); expect(result.status).toBe(200);
  expect((await admin.query("SELECT state FROM space_storage_contributions WHERE source_id=ANY($1::text[])", [ids])).rows.map(row => row.state)).toEqual(["active", "active"]);
  expect((await (await f.request(`${f.root}/usage`)).json()).space_used_bytes).toBe(256);
  expect((await admin.query("SELECT details FROM space_library_audit_events WHERE space_id=$1 AND action='library.item.restored' ORDER BY id", [f.spaceId])).rows.map(row => row.details)).toEqual([{}, { bulk: true }, { bulk: true }]);
});

it("rejects inaccessible bulk items and rolls back edits when audit persistence fails", async () => {
  const f = await fixture(), id = await f.item("Shared"), conversation = randomUUID();
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.member.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.member.user.id]);
  const privateId = await f.item("Secret", { conversation });
  const request = (items: unknown, extra: Record<string, unknown> = {}) => f.request(`${f.root}/items/bulk`, f.owner.token, "", { action: "favorite", items, ...extra });
  expect((await request([{ id, version: 1 }, { id: privateId, version: 1 }])).status).toBe(404);
  expect((await request([{ id, version: 2 }])).status).toBe(409);
  expect((await request([{ id, version: 1 }, { id, version: 1 }])).status).toBe(400);
  expect((await request([{ id, version: 1 }], { action: "set_location", location_override: [] })).status).toBe(400);
  await admin.query("REVOKE INSERT ON space_library_audit_events FROM misty_library_test");
  try { expect((await request([{ id, version: 1 }])).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_library_audit_events TO misty_library_test"); }
  expect((await admin.query("SELECT favorite,version FROM space_library_items WHERE id=$1", [id])).rows[0]).toEqual({ favorite: false, version: "1" });
});

it("runs album CRUD, cover selection, membership, custom ordering and capture-date sorting", async () => {
  const f = await fixture(), base = `${f.root}/albums`, ids = [await f.item("First"), await f.item("Second")];
  const created = await f.request(`/api${base}`, f.owner.token, "", { name: " Album ", description: " Description " });
  expect(created.status, await created.clone().text()).toBe(201); const album = await created.json(), path = `${base}/${album.id}`;
  expect(album).toMatchObject({ name: "Album", description: "Description", version: 1, item_count: 0, position: 0, view_mode: "grid", sort_mode: "custom" });
  expect(album).not.toHaveProperty("folder_id");
  expect((await (await f.request(base)).json()).albums).toEqual([album]);
  expect((await f.request(`${path}/items`, f.owner.token, "", { item_ids: [ids[0], ` ${ids[1]} `, ids[0]] })).status).toBe(204);
  const updated = await f.request(`/v1${path}`, f.owner.token, "", { version: 2, name: "Renamed", cover_item_id: ids[0] }, "PATCH");
  expect(updated.status, await updated.clone().text()).toBe(200); expect(await updated.json()).toMatchObject({ version: 3, item_count: 2, cover_item_id: ids[0] });
  const reordered = await f.request(`${path}/order`, f.owner.token, "", { version: 3, item_ids: [...ids].reverse() });
  expect(reordered.status).toBe(200); expect((await reordered.json()).version).toBe(4);
  expect((await (await f.request(`${path}/items`)).json()).items.map((item: { id: string }) => item.id)).toEqual([...ids].reverse());
  await admin.query("UPDATE space_library_items SET date_override='2025-01-01T00:00:00Z' WHERE id=$1", [ids[0]]);
  const organized = await f.request(`${path}/organization`, f.owner.token, "", { version: 4, view_mode: "list", sort_mode: "oldest", position: 7 }, "PUT");
  expect(organized.status).toBe(200); expect(await organized.json()).toMatchObject({ version: 5, view_mode: "list", sort_mode: "oldest", position: 7 });
  expect((await (await f.request(`${path}/items`)).json()).items.map((item: { id: string }) => item.id)).toEqual(ids);
  expect((await f.request(`${path}/items/${ids[1]}`, f.owner.token, "", undefined, "DELETE")).status).toBe(204);
  expect((await (await f.request(path)).json()).item_count).toBe(1);
  expect((await f.request(`${path}?version=6`, f.owner.token, "", undefined, "DELETE")).status).toBe(204);
  expect((await f.request(path)).status).toBe(404);
  expect((await admin.query("SELECT id FROM space_library_items WHERE id=ANY($1::text[])", [ids])).rowCount).toBe(2);
});

it("maintains folder ancestry, counts, versions and cascading deletion without deleting albums", async () => {
  const f = await fixture(), base = `${f.root}/album-folders`;
  const parentResponse = await f.request(base, f.owner.token, "", { name: "Parent" }); expect(parentResponse.status, await parentResponse.clone().text()).toBe(201); const parent = await parentResponse.json();
  const child = await (await f.request(`/v1${base}`, f.owner.token, "", { name: "Child", parent_folder_id: parent.id })).json();
  expect(child).toMatchObject({ parent_folder_id: parent.id, version: 1, position: 0 });
  expect((await (await f.request(base)).json()).folders.find((folder: { id: string }) => folder.id === parent.id)).toMatchObject({ folder_count: 1, album_count: 0 });
  expect((await f.request(`${base}/${parent.id}`, f.owner.token, "", { version: 1, name: "Cycle", parent_folder_id: child.id }, "PATCH")).status).toBe(400);
  const edit = { version: 1, name: "Updated child", parent_folder_id: parent.id, position: 4 };
  expect((await f.request(`${base}/${child.id}`, f.owner.token, "", edit, "PATCH")).status).toBe(200);
  expect((await f.request(`${base}/${child.id}`, f.owner.token, "", edit, "PATCH")).status).toBe(409);
  const album = await (await f.request(`${f.root}/albums`, f.owner.token, "", { name: "Kept" })).json();
  expect((await f.request(`${f.root}/albums/${album.id}/organization`, f.owner.token, "", { version: 1, folder_id: child.id, view_mode: "grid", sort_mode: "newest" }, "PUT")).status).toBe(200);
  expect((await (await f.request(base)).json()).folders.find((folder: { id: string }) => folder.id === child.id).album_count).toBe(1);
  expect((await f.request(`/api${base}/${parent.id}?version=1`, f.owner.token, "", undefined, "DELETE")).status).toBe(204);
  expect((await (await f.request(base)).json()).folders).toEqual([]);
  expect(await (await f.request(`${f.root}/albums/${album.id}`)).json()).not.toHaveProperty("folder_id");
  const a = await (await f.request(base, f.owner.token, "", { name: "A" })).json(), b = await (await f.request(base, f.owner.token, "", { name: "B" })).json();
  const moves = await Promise.all([f.request(`${base}/${a.id}`, f.owner.token, "", { version: 1, name: "A", parent_folder_id: b.id }, "PATCH"), f.request(`${base}/${b.id}`, f.owner.token, "", { version: 1, name: "B", parent_folder_id: a.id }, "PATCH")]);
  expect(moves.map(response => response.status).sort()).toEqual([200, 400]);
});

it("filters private album counts, covers and items and prevents unauthorized membership changes", async () => {
  const f = await fixture(), conversation = randomUUID(), shared = await f.item("Shared");
  await admin.query("INSERT INTO space_conversations(id,space_id,title,created_by_user_id) VALUES($1,$2,'Private',$3)", [conversation, f.spaceId, f.owner.user.id]);
  await admin.query("INSERT INTO space_conversation_members(conversation_id,user_id) VALUES($1,$2)", [conversation, f.owner.user.id]);
  const privateId = await f.item("Private", { conversation }), album = await (await f.request(`${f.root}/albums`, f.owner.token, "", { name: "Album" })).json(), path = `${f.root}/albums/${album.id}`;
  expect((await f.request(`${path}/items`, f.owner.token, "", { item_ids: [shared, privateId] })).status).toBe(204);
  expect((await f.request(path, f.owner.token, "", { version: 2, name: "Album", cover_item_id: privateId }, "PATCH")).status).toBe(200);
  const member = await (await f.request(path, f.member.token)).json(); expect(member.item_count).toBe(1); expect(member).not.toHaveProperty("cover_item_id");
  expect((await (await f.request(`${path}/items`, f.member.token)).json()).items.map((item: { id: string }) => item.id)).toEqual([shared]);
  expect((await f.request(`${path}/items`, f.member.token, "", { item_ids: [privateId] })).status).toBe(400);
  expect((await f.request(`${path}/items/${privateId}`, f.member.token, "", undefined, "DELETE")).status).toBe(404);
  expect((await f.request(`${path}/order`, f.member.token, "", { version: 3, item_ids: [privateId] })).status).toBe(400);
  expect((await f.request(path, f.outsider.token)).status).toBe(403);
  await admin.query("UPDATE space_library_items SET hidden=TRUE WHERE id=$1", [privateId]);
  const owner = await (await f.request(path)).json(); expect(owner.item_count).toBe(1); expect(owner).not.toHaveProperty("cover_item_id");
});

it("rejects organization conflicts, cross-Space folders and revoked edits and rolls back failed audits", async () => {
  const f = await fixture(), base = `${f.root}/albums`, album = await (await f.request(base, f.owner.token, "", { name: "Unique" })).json();
  expect((await f.request(base, f.owner.token, "", { name: "Unique" })).status).toBe(409);
  expect((await f.request(`${base}/${album.id}?version=2`, f.owner.token, "", undefined, "DELETE")).status).toBe(409);
  expect((await f.request(`${base}/${album.id}/organization`, f.owner.token, "", { version: 1, folder_id: "missing", view_mode: "grid", sort_mode: "custom" }, "PUT")).status).toBe(400);
  const foreignSpace = (await createSpaceRepository(application).create(f.owner.user.id, { name: "Other", template_id: "blank", integration_providers: [] }, "")).space.id, foreignFolder = randomUUID();
  await admin.query("INSERT INTO space_album_folders(id,space_id,name,created_by_user_id) VALUES($1,$2,'Foreign',$3)", [foreignFolder, foreignSpace, f.owner.user.id]);
  expect((await f.request(`${base}/${album.id}/organization`, f.owner.token, "", { version: 1, folder_id: foreignFolder, view_mode: "grid", sort_mode: "custom" }, "PUT")).status).toBe(400);
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'library.edit','deny',$2)", [f.spaceId, f.member.user.id]);
  expect((await f.request(base, f.member.token, "", { name: "Denied" })).status).toBe(403);
  await admin.query("REVOKE INSERT ON space_library_audit_events FROM misty_library_test");
  try { expect((await f.request(`${base}/${album.id}`, f.owner.token, "", { version: 1, name: "Rollback" }, "PATCH")).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON space_library_audit_events TO misty_library_test"); }
  expect((await admin.query("SELECT name,version FROM space_albums WHERE id=$1", [album.id])).rows[0]).toEqual({ name: "Unique", version: "1" });
  await admin.query("INSERT INTO space_albums(id,space_id,name,created_by_user_id) SELECT $1||g::text,$2,'Limit '||g::text,$3 FROM generate_series(1,499) g", [randomUUID(), f.spaceId, f.owner.user.id]);
  expect((await f.request(base, f.owner.token, "", { name: "Beyond limit" })).status).toBe(400);
});

it("creates and lists rule-based groups and evaluates the existing filters without exposing hidden items", async () => {
  const f = await fixture(), base = `${f.root}/groups`, selected = await f.item("Photo Shot", { favorite: true, tags: ["Trip"] });
  await f.item("Other", { favorite: false }); await f.item("Hidden Shot", { hidden: true, favorite: true, tags: ["Trip"] });
  await admin.query("UPDATE library_files SET intrinsic_metadata=$2::jsonb WHERE id=(SELECT file_id FROM space_library_items WHERE id=$1)", [selected, JSON.stringify({ server_detected_mime_type: "image/jpeg" })]);
  const album = await (await f.request(`${f.root}/albums`, f.owner.token, "", { name: "Selected" })).json();
  await f.request(`${f.root}/albums/${album.id}/items`, f.owner.token, "", { item_ids: [selected] });
  const rules = { all: [{ field: "favorite", op: "is", value: true }, { field: "tag", op: "contains", value: "Trip" }, { field: "mime", op: "prefix", value: "image/" }, { field: "filename", op: "contains", value: "Shot" }, { field: "album", op: "in", value: album.id }] };
  const created = await f.request(`/api${base}`, f.owner.token, "", { name: " Trip photos ", rules }); expect(created.status, await created.clone().text()).toBe(201); const group = await created.json();
  expect(group).toMatchObject({ name: "Trip photos", rules, version: 1 }); expect((await (await f.request(base)).json()).groups).toEqual([group]);
  expect((await (await f.request(`/v1${base}/${group.id}/items`)).json()).items.map((item: { id: string }) => item.id)).toEqual([selected]);
  const hidden = await (await f.request(base, f.owner.token, "", { name: "Hidden", rules: { all: [{ field: "hidden", op: "is", value: true }] } })).json();
  expect((await (await f.request(`${base}/${hidden.id}/items`)).json()).items).toEqual([]);
  const all = await (await f.request(base, f.owner.token, "", { name: "All" })).json(); expect(all.rules).toEqual({ all: null });
  expect((await f.request(`${base}/missing/items`)).status).toBe(404);
  expect((await f.request(`${base}/${group.id}/items`, f.outsider.token)).status).toBe(403);
  expect((await f.request(base, f.owner.token, "", { name: "Invalid", rules: { all: [{ field: "favorite", op: "is", value: "true" }] } })).status).toBe(400);
});
