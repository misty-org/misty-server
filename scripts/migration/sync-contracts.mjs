import { execFileSync } from "node:child_process";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const archiveIndex = process.argv.indexOf("--archive");
if (archiveIndex !== -1 && (!process.argv[archiveIndex + 1] || process.argv[archiveIndex + 1].startsWith("--"))) throw new Error("--archive requires a reviewed public package path");
const archive = archiveIndex === -1 ? null : resolve(process.argv[archiveIndex + 1]);
const source = resolve(root, "../misty-sdk/packages/contracts");
const destination = join(root, "third-party/misty-contracts");
const temporary = await mkdtemp(join(tmpdir(), "misty-public-contracts-"));
try {
  // A checked immutable archive avoids racing the SDK task's next package build.
  const result = JSON.parse(execFileSync("npm", ["pack", ...(archive ? [archive] : []), "--json", "--ignore-scripts", "--pack-destination", temporary], { cwd: archive ? root : source, encoding: "utf8" }));
  const packed = result[0];
  if (packed?.name !== "@misty/contracts" || !packed.files.every(({ path }) => path === "package.json" || path.startsWith("dist/") || /^(LICENSE|README)(\.|$)/.test(path))) {
    throw new Error("Unexpected files in public contracts package");
  }
  const snapshot = { name: packed.name, version: packed.version, integrity: packed.integrity };
  let existing;
  try { existing = JSON.parse(await readFile(join(destination, ".snapshot.json"), "utf8")); }
  catch (error) { if (error.code !== "ENOENT") throw error; }
  if (process.argv.includes("--check")) {
    if (JSON.stringify(snapshot) !== JSON.stringify(existing)) throw new Error("Server contracts snapshot differs from the built public SDK package; run npm run contracts:sync");
    console.log(`Server snapshot matches the ${archive ? "reviewed archive" : "built public contracts package"}.`);
  } else {
    execFileSync("tar", ["-xzf", join(temporary, packed.filename), "-C", temporary]);
    if (existing) await rm(destination, { recursive: true });
    await mkdir(join(root, "third-party"), { recursive: true });
    await cp(join(temporary, "package"), destination, { recursive: true, force: false, errorOnExist: true });
    await writeFile(join(destination, ".snapshot.json"), JSON.stringify(snapshot, null, 2) + "\n");
    console.log(`Synced public ${packed.name}@${packed.version}; no private SDK build dependency.`);
  }
} finally { await rm(temporary, { recursive: true, force: true }); }
