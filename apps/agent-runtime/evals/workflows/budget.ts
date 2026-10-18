// The eval's spending cap, read from the AI Gateway's own credit balance, so
// every model, search and judge call counts. Other use of the same key counts
// too, which only makes the cap stricter.
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const envFile = resolve(import.meta.dirname, "../../../../.env/dev/integrations/ai.env");

export function gatewayKey(): string {
  if (process.env.AI_GATEWAY_API_KEY) return process.env.AI_GATEWAY_API_KEY.trim();
  if (!existsSync(envFile)) return "";
  const line = readFileSync(envFile, "utf8").split("\n").find((item) => item.startsWith("AI_GATEWAY_API_KEY="));
  return (line?.slice("AI_GATEWAY_API_KEY=".length) ?? "").replace(/^(['"])(.*)\1$/, "$2").trim();
}

export const gatewayBase = () => (process.env.AI_GATEWAY_BASE_URL || "https://ai-gateway.vercel.sh/v1").replace(/\/$/, "");

/** The balance, retried through a brief network drop (a minute at most) before giving up. */
async function steadyBalance(): Promise<number> {
  for (let attempt = 1; ; attempt++) {
    try {
      return await gatewayBalance();
    } catch (error) {
      if (attempt >= 6) throw error;
      await new Promise((done) => setTimeout(done, 10_000));
    }
  }
}

export async function gatewayBalance(): Promise<number> {
  const key = gatewayKey();
  if (!key) throw new Error("No AI_GATEWAY_API_KEY to read the credit balance.");
  const response = await fetch(`${gatewayBase()}/credits`, { headers: { Authorization: `Bearer ${key}` } });
  if (!response.ok) throw new Error(`credit balance: ${response.status}`);
  return Number(((await response.json()) as { balance: string }).balance);
}

export class Budget {
  private constructor(
    private readonly start: number,
    readonly cap: number,
    private latest: number,
  ) {}

  /** Defaults to $7.50, leaving room for the one task that may run past the check. */
  static async open(cap = Number(process.env.MISTY_WORKFLOW_EVAL_BUDGET || 7.5)): Promise<Budget> {
    const balance = await gatewayBalance();
    return new Budget(balance, cap, balance);
  }

  get spent() {
    return this.start - this.latest;
  }

  /** Reads the balance; true while another task fits under the cap. */
  async allows(): Promise<boolean> {
    this.latest = await steadyBalance();
    return this.spent < this.cap && this.latest > 0.5;
  }

  /** What one stretch of work cost, from two balance reads. */
  async since(before: number): Promise<number> {
    this.latest = await steadyBalance();
    return Math.max(0, before - this.latest);
  }

  get balance() {
    return this.latest;
  }
}
