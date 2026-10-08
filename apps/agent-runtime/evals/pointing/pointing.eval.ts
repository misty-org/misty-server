// Scores how precisely models point at screen controls, through the same
// teaching prompt, image labels and model route production uses.
//
//   MISTY_POINTING_EVAL_MODELS=openai/gpt-6-astra,anthropic/claude-sonnet-5-5 \
//   MISTY_POINTING_EVAL_REASONING=low npm run eval:pointing
//
// Fixtures: evals/pointing/fixtures (or MISTY_POINTING_EVAL_FIXTURES); make
// them with capture-fixtures.ts. See README.md.
import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { expect, it } from "vitest";
import { companionImageParts } from "../../src/companion-images.js";
import { generateTextCall } from "../../src/model-calls.js";
import { scoreReply, summarize, validateFixture, type PointingResult } from "./score.js";

const here = resolve(import.meta.dirname);
const prompts = resolve(here, "../../../../internal/platform/httpapi");
/** Exactly what a teaching turn's system prompt carries about the companion. */
const system = [
  readFileSync(join(prompts, "companion_teach_prompt.txt"), "utf8"),
  readFileSync(join(prompts, "companion_teach_turn.txt"), "utf8"),
].join("");
const fixturesDir = resolve(process.env.MISTY_POINTING_EVAL_FIXTURES || join(here, "fixtures"));
const models = (process.env.MISTY_POINTING_EVAL_MODELS || "")
  .split(",")
  .map((model) => model.trim())
  .filter(Boolean);
const reasoning = (process.env.MISTY_POINTING_EVAL_REASONING || "low") as "low";

function fixtures() {
  return readdirSync(fixturesDir)
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => validateFixture(JSON.parse(readFileSync(join(fixturesDir, name), "utf8"))));
}

it.skipIf(!models.length)("points at the right control", async () => {
  const cases = fixtures();
  expect(cases.length, `no fixtures in ${fixturesDir}`).toBeGreaterThan(0);
  const results: PointingResult[] = [];
  for (const model of models) {
    for (const fixture of cases) {
      const bytes = readFileSync(join(fixturesDir, fixture.image));
      const parts = companionImageParts([
        {
          id: fixture.id,
          name: "screen1",
          screen: "screen1",
          primary: true,
          width: fixture.width,
          height: fixture.height,
          mime_type: fixture.mimeType,
          data_url: `data:${fixture.mimeType};base64,${bytes.toString("base64")}`,
          content_hash: createHash("sha256").update(bytes).digest("hex"),
        },
      ]);
      const content = parts.map((part) =>
        part.type === "text"
          ? { type: "text" as const, text: part.text }
          : {
              type: "image" as const,
              mediaType: fixture.mimeType,
              data: (part.data as { data: string }).data,
            },
      );
      const started = performance.now();
      try {
        const reply = await generateTextCall({
          route: { provider: "instance", reasoning },
          model,
          system,
          messages: [
            { role: "user", content: [...content, { type: "text", text: fixture.question }] },
          ],
          maxOutputTokens: 2_200,
        });
        results.push({
          ...scoreReply(fixture, model, reply.text, performance.now() - started),
          reply: reply.text,
        });
      } catch (error) {
        results.push({
          fixture: fixture.id,
          model,
          pointed: false,
          hit: false,
          latencyMs: performance.now() - started,
          error: error instanceof Error ? error.message : String(error),
        });
      }
    }
  }
  const summary = summarize(results);
  const report = join(here, `report-${new Date().toISOString().replace(/[:.]/g, "-")}.json`);
  writeFileSync(report, JSON.stringify({ reasoning, summary, results }, null, 2));
  console.table(summary);
  console.log(`Pointing eval report: ${report}`);
});
