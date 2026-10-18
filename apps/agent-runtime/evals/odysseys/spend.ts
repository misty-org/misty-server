// What the Odysseys campaign spends on Misty's own Anthropic key, which the AI Gateway
// balance never sees. Each run's agent tokens come from the local billing ledger
// (../workflows/usage.ts), the judge's from its own calls; a ledger file keeps the total
// across passes so the campaign stops at its cap (docs/plans/odysseys-campaign.md).
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

/** Dollars per million tokens. Cached input bills at a tenth of input. */
const prices: Record<string, { input: number; cached: number; output: number }> = {
  "anthropic/claude-haiku-5.5": { input: 0.1, cached: 0.01, output: 0.5 },
  "anthropic/claude-sonnet-5.5": { input: 2, cached: 0.2, output: 10 },
  "anthropic/claude-opus-5.5": { input: 4, cached: 0.4, output: 20 },
};
/** Serper charges per search, outside any model. */
const searchUsd = 0.001;

export function tokenUsd(model: string, input: number, cached: number, output: number): number {
  const price = prices[model] ?? prices["anthropic/claude-sonnet-5.5"]!;
  return ((input - cached) * price.input + cached * price.cached + output * price.output) / 1_000_000;
}

export function searchesUsd(count: number): number {
  return count * searchUsd;
}

export class Ledger {
  private total: number;

  constructor(private readonly file: string, readonly cap: number) {
    this.total = existsSync(file) ? Number((JSON.parse(readFileSync(file, "utf8")) as { spentUsd?: number }).spentUsd ?? 0) : 0;
  }

  get spent(): number {
    return this.total;
  }

  allows(): boolean {
    return this.total < this.cap;
  }

  add(usd: number): void {
    this.total += usd;
    mkdirSync(dirname(this.file), { recursive: true });
    writeFileSync(this.file, JSON.stringify({ spentUsd: Number(this.total.toFixed(4)), updatedAt: new Date().toISOString() }, null, 2));
  }
}
