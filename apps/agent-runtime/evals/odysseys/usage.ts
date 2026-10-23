// What a run's model turns used, read from the local billing ledger: each turn
// settles its measured input, cached-input and output tokens there. Prices
// come from the AI Gateway's model list, so the cache report shows what
// caching saved in dollars, not only as a share of tokens.
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

export interface RunUsage {
  turns: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  /** What the cached part of the input would have cost at the full input price, minus what it cost. */
  cacheSavedUsd: number;
}

interface Price {
  input: number;
  cachedInput: number;
}

const envFile = resolve(import.meta.dirname, "../../../../.env/dev/integrations/ai.env");

function gatewayKey(): string {
  if (process.env.AI_GATEWAY_API_KEY) return process.env.AI_GATEWAY_API_KEY.trim();
  if (!existsSync(envFile)) return "";
  const line = readFileSync(envFile, "utf8").split("\n").find((item) => item.startsWith("AI_GATEWAY_API_KEY="));
  return (line?.slice("AI_GATEWAY_API_KEY=".length) ?? "").replace(/^(['"])(.*)\1$/, "$2").trim();
}

const gatewayBase = () => (process.env.AI_GATEWAY_BASE_URL || "https://ai-gateway.vercel.sh/v1").replace(/\/$/, "");

const container = process.env.MISTY_BILLING_DB_CONTAINER ?? "misty-billing-postgres-1";
let prices: Promise<Map<string, Price>> | undefined;

/** Per-token prices for every Gateway model, fetched once per pass. */
function modelPrices(): Promise<Map<string, Price>> {
  prices ??= (async () => {
    const response = await fetch(`${gatewayBase()}/models`, { headers: { Authorization: `Bearer ${gatewayKey()}` } });
    const body = (await response.json()) as { data?: Array<{ id: string; pricing?: { input?: string; input_cache_read?: string } }> };
    return new Map((body.data ?? []).map((model) => [model.id, {
      input: Number(model.pricing?.input ?? 0),
      cachedInput: Number(model.pricing?.input_cache_read ?? model.pricing?.input ?? 0),
    }]));
  })();
  return prices;
}

function ledger(invocationId: string): Array<{ model: string; input: number; cached: number; output: number }> {
  if (!/^invocation_[0-9a-f-]{36}$/.test(invocationId)) return [];
  const sql = `SELECT coalesce(request->'usage'->>'model',''), coalesce(request->'usage'->'units'->>'input_tokens','0'),
    coalesce(request->'usage'->'units'->>'cached_input_tokens','0'), coalesce(request->'usage'->'units'->>'output_tokens','0')
    FROM billing.usage_receipts WHERE action='settle' AND request->>'operation'='agent.model' AND key LIKE 'agent-runtime:${invocationId}:%'`;
  const rows = execFileSync("docker", ["exec", container, "psql", "-U", "billing_owner", "-d", "misty_billing", "-t", "-A", "-F", "\t", "-c", sql]).toString();
  return rows.split("\n").filter(Boolean).map((line) => {
    const [model = "", input = "0", cached = "0", output = "0"] = line.split("\t");
    return { model, input: Number(input), cached: Number(cached), output: Number(output) };
  });
}

/** The run's model usage; settlement can trail the run's last event by a moment. */
export async function runUsage(invocationId: string): Promise<RunUsage | undefined> {
  let turns = ledger(invocationId);
  if (!turns.length) {
    await new Promise((done) => setTimeout(done, 2_000));
    turns = ledger(invocationId);
  }
  if (!turns.length) return undefined;
  const table = await modelPrices().catch(() => new Map<string, Price>());
  let saved = 0;
  for (const turn of turns) {
    const price = table.get(turn.model);
    if (price) saved += turn.cached * (price.input - price.cachedInput);
  }
  return {
    turns: turns.length,
    inputTokens: turns.reduce((sum, turn) => sum + turn.input, 0),
    cachedInputTokens: turns.reduce((sum, turn) => sum + turn.cached, 0),
    outputTokens: turns.reduce((sum, turn) => sum + turn.output, 0),
    cacheSavedUsd: Number(saved.toFixed(5)),
  };
}
