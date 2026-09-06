import { readdir, readFile, mkdir, writeFile } from "node:fs/promises";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
async function files(directory, suffix) {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(entries.map(async (entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? files(path, suffix) : path.endsWith(suffix) ? [path] : [];
  }));
  return nested.flat().sort();
}

const sourceFiles = await files(join(root, "internal"), ".go");
const routeRegistrations = [];
for (const path of sourceFiles) {
  const lines = (await readFile(path, "utf8")).split("\n");
  lines.forEach((expression, index) => {
    if (/\.(Get|Post|Put|Patch|Delete|Head|Options|MethodFunc|Method|Handle|HandleFunc|Mount|Route)\(/.test(expression)) {
      routeRegistrations.push({ source: relative(root, path), line: index + 1, expression: expression.trim(), owner: "go", verified: false });
    }
  });
}
const startup = await readFile(join(root, "internal/app/run.go"), "utf8");
const jobs = [...startup.matchAll(/WorkerFunc\(func\(ctx context\.Context\) \{ (.*?) \}\)/g)]
  .map((match) => ({ expression: match[1], owner: "go", verified: false }));
const report = {
  description: "Static discovery candidates; effective runtime routes and aliases require parity verification.",
  implementationFiles: sourceFiles.filter((path) => !path.endsWith("_test.go")).map((path) => relative(root, path)),
  testFiles: (await files(join(root, "test"), ".go")).map((path) => relative(root, path)),
  migrations: (await files(join(root, "internal/platform/postgres/migrations"), ".sql")).map((path) => relative(root, path)),
  commands: (await files(join(root, "cmd"), ".go")).map((path) => ({ source: relative(root, path), owner: "go", verified: false })),
  jobs,
  routeRegistrations,
  mountedRouteBaseline: (await readFile(join(root, "test/contract/http/app/routes.golden"), "utf8"))
    .trim().split("\n").map((route) => ({ route, owner: "go", verified: false })),
};
await mkdir(join(root, "docs/migration"), { recursive: true });
await writeFile(join(root, "docs/migration/go-inventory.json"), JSON.stringify(report, null, 2) + "\n");
console.log(JSON.stringify({ implementationFiles: report.implementationFiles.length, tests: report.testFiles.length, migrations: report.migrations.length, jobs: jobs.length, routeCandidates: routeRegistrations.length }));
