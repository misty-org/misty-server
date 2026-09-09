import { readFile, writeFile } from "node:fs/promises";

// @ai-sdk/workflow 1.0.67 reconstructs every serialized tool with draft-7
// Ajv. Misty's canonical SDK contracts use 2020-12. Keep their validation
// intact across the durable step boundary instead of stripping $schema or
// format constraints. Patch both entry points: Nitro's workflow compiler
// consumes source while ordinary Node consumers use dist.
const root = new URL("../node_modules/@ai-sdk/workflow/", import.meta.url);
const pkg = JSON.parse(await readFile(new URL("package.json", root), "utf8"));
if (pkg.version !== "1.0.67") throw new Error("Review the workflow schema patch before upgrading @ai-sdk/workflow.");
const marker = "// Misty: preserve tool schema dialects and formats.";
for (const [file, quote] of [["src/serializable-schema.ts", "'"], ["dist/index.js", '"']]) {
  const path = new URL(file, root);
  const source = await readFile(path, "utf8");
  if (source.includes(marker)) continue;
  const replacements = [
    [`import Ajv from ${quote}ajv${quote};`, `import Ajv from ${quote}ajv${quote};\nimport Ajv2020 from 'ajv/dist/2020.js';\nimport addFormats from 'ajv-formats';\n${marker}`],
    ["const ajv = new Ajv();", "const draft7 = addFormats(new Ajv());\n  const draft2020 = addFormats(new Ajv2020());"],
    ["const validateFn = ajv.compile(t.inputSchema);", "const ajv = t.inputSchema.$schema === 'https://json-schema.org/draft/2020-12/schema' ? draft2020 : draft7;\n      const validateFn = ajv.compile(t.inputSchema);"],
  ];
  let patched = source;
  for (const [from, to] of replacements) {
    if (patched.split(from).length !== 2) throw new Error(`Unexpected workflow source in ${file}; refusing partial patch.`);
    patched = patched.replace(from, to);
  }
  await writeFile(path, patched);
}
