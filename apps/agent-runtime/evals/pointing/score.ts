/** One question about one screenshot, with the box a correct point lands in. */
export interface PointingFixture {
  id: string;
  /** Image file next to the fixture JSON, sent exactly as the companion sends a display. */
  image: string;
  mimeType: "image/jpeg" | "image/png";
  width: number;
  height: number;
  question: string;
  /** The correct control, in the image's pixels. */
  target: { x: number; y: number; width: number; height: number };
}

export interface PointingResult {
  fixture: string;
  model: string;
  /** The model pointed somewhere (not [POINT:none] and not missing). */
  pointed: boolean;
  /** The point landed inside the target box. */
  hit: boolean;
  /** Pixels from the point to the target's center; undefined without a point. */
  distance?: number;
  latencyMs: number;
  error?: string;
  /** The model's whole answer, for reading what it said. */
  reply?: string;
}

const pointTag = /\[POINT:([^\]\r\n]*)\]/g;

/** The last POINT the reply gives, as the desktop parses it; undefined for none. */
export function replyPoint(text: string) {
  let point: { x: number; y: number; screen?: string } | undefined;
  for (const match of text.matchAll(pointTag)) {
    const body = match[1] ?? "";
    if (body.trim().toLowerCase() === "none") {
      point = undefined;
      continue;
    }
    const parsed = /^(\d+(?:\.\d+)?),\s*(\d+(?:\.\d+)?):([^\r\n]{1,120}?)(?::(screen\d+))?$/.exec(
      body,
    );
    if (parsed) point = { x: Number(parsed[1]), y: Number(parsed[2]), screen: parsed[4] };
  }
  return point;
}

export function scoreReply(
  fixture: PointingFixture,
  model: string,
  text: string,
  latencyMs: number,
): PointingResult {
  const point = replyPoint(text);
  if (!point || (point.screen && point.screen !== "screen1"))
    return { fixture: fixture.id, model, pointed: false, hit: false, latencyMs };
  const { target } = fixture;
  const hit =
    point.x >= target.x &&
    point.x <= target.x + target.width &&
    point.y >= target.y &&
    point.y <= target.y + target.height;
  const distance = Math.hypot(
    point.x - (target.x + target.width / 2),
    point.y - (target.y + target.height / 2),
  );
  return { fixture: fixture.id, model, pointed: true, hit, distance, latencyMs };
}

const median = (values: number[]) => {
  if (!values.length) return undefined;
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1]! + sorted[middle]!) / 2;
};

/** Hit rate, pointing rate, median miss distance and latency per model. */
export function summarize(results: PointingResult[]) {
  const models = [...new Set(results.map((result) => result.model))];
  return models.map((model) => {
    const mine = results.filter((result) => result.model === model);
    const answered = mine.filter((result) => !result.error);
    return {
      model,
      fixtures: mine.length,
      errors: mine.length - answered.length,
      hitRate: answered.length ? answered.filter((r) => r.hit).length / answered.length : 0,
      pointRate: answered.length ? answered.filter((r) => r.pointed).length / answered.length : 0,
      medianDistance: median(
        answered.flatMap((r) => (r.distance === undefined ? [] : [r.distance])),
      ),
      medianLatencyMs: median(answered.map((r) => r.latencyMs)),
    };
  });
}

/** Rejects a fixture whose target cannot be scored against its image. */
export function validateFixture(value: unknown): PointingFixture {
  const fixture = value as PointingFixture;
  const finite = (n: unknown) => typeof n === "number" && Number.isFinite(n);
  const { target } = fixture ?? {};
  if (
    !fixture ||
    typeof fixture.id !== "string" ||
    typeof fixture.image !== "string" ||
    (fixture.mimeType !== "image/jpeg" && fixture.mimeType !== "image/png") ||
    typeof fixture.question !== "string" ||
    !fixture.question.trim() ||
    ![fixture.width, fixture.height].every(finite) ||
    !target ||
    ![target.x, target.y, target.width, target.height].every(finite) ||
    target.width <= 0 ||
    target.height <= 0 ||
    target.x < 0 ||
    target.y < 0 ||
    target.x + target.width > fixture.width ||
    target.y + target.height > fixture.height
  )
    throw new Error(`Invalid pointing fixture ${JSON.stringify(fixture?.id ?? value)}`);
  return fixture;
}
