// The leak check (docs/agents/browser.md, "Memory"). Closed agent tabs and windows
// once left their WebKit page processes running, and a day of runs reached 250 processes for 54
// open pages. Every pass lists the computer's page processes before it starts and again after its
// runs end, once their tabs have slept: a new one that no open view uses was left behind.

type Bridge = { url: string; token: string };

interface PageProcesses {
  /** Every WebKit page process on this computer. */
  all: number[];
  /** The ones Misty's open web views use. */
  used: number[];
}

export async function pageProcesses(bridge: Bridge): Promise<PageProcesses | undefined> {
  const response = await fetch(`${bridge.url}/processes`, {
    method: "POST",
    headers: { authorization: `Bearer ${bridge.token}`, "content-type": "application/json" },
    body: "{}",
  }).catch(() => undefined);
  const body = (await response?.json().catch(() => undefined)) as
    | { ok?: boolean; result?: { webProcesses?: unknown; views?: Array<{ pid?: number }> } }
    | undefined;
  const all = body?.result?.webProcesses;
  if (!body?.ok || !Array.isArray(all)) return undefined;
  return { all: all as number[], used: (body.result?.views ?? []).map((view) => view.pid ?? 0) };
}

/** One page process WebKit may keep for a page it just closed. */
const CACHE = 1;

/** The processes started since `before` that no open view uses. */
export function leftBehind(before: PageProcesses, after: PageProcesses): number[] {
  const old = new Set(before.all);
  const used = new Set(after.used);
  return after.all.filter((pid) => !old.has(pid) && !used.has(pid));
}

/**
 * After a pass: waits up to a minute for the runs' tabs to sleep, then says how many page
 * processes the pass left behind (0 when none, allowing WebKit's one cached process).
 */
export async function leakedProcesses(bridge: Bridge, before: PageProcesses, waitMs = 60_000): Promise<number> {
  const deadline = Date.now() + waitMs;
  for (;;) {
    const now = await pageProcesses(bridge);
    if (!now) return 0;
    const leaked = Math.max(0, leftBehind(before, now).length - CACHE);
    if (leaked === 0 || Date.now() > deadline) return leaked;
    await new Promise((resolve) => setTimeout(resolve, 5_000));
  }
}
