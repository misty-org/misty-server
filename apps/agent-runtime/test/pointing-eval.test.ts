import { describe, expect, it } from "vitest";
import { captureSize, targetBox } from "../evals/pointing/capture-fixtures.js";
import { replyPoint, scoreReply, summarize, validateFixture } from "../evals/pointing/score.js";

const fixture = validateFixture({
  id: "commit",
  image: "capture.jpg",
  mimeType: "image/jpeg",
  width: 1188,
  height: 768,
  question: "where do I click Commit?",
  target: { x: 300, y: 40, width: 60, height: 20 },
});

describe("pointing eval scoring", () => {
  it("reads the point the desktop would show", () => {
    expect(replyPoint("there [POINT:330,50:click Commit:screen1] [GUIDE:1/2]")).toEqual({
      x: 330,
      y: 50,
      screen: "screen1",
    });
    expect(replyPoint("no [POINT:330,50:x:screen1] [POINT:none]")).toBeUndefined();
  });

  it("scores hits, misses, other screens and silence", () => {
    expect(scoreReply(fixture, "m", "[POINT:330,50:Commit:screen1]", 900)).toMatchObject({
      pointed: true,
      hit: true,
      distance: 0,
    });
    const miss = scoreReply(fixture, "m", "[POINT:400,50:Commit:screen1]", 900);
    expect(miss).toMatchObject({ pointed: true, hit: false });
    expect(miss.distance).toBeCloseTo(70);
    expect(scoreReply(fixture, "m", "[POINT:330,50:Commit:screen2]", 900).pointed).toBe(false);
    expect(scoreReply(fixture, "m", "[POINT:none]", 900).pointed).toBe(false);
  });

  it("summarizes per model without counting errors as misses", () => {
    const summary = summarize([
      scoreReply(fixture, "a", "[POINT:330,50:x:screen1]", 1000),
      scoreReply(fixture, "a", "[POINT:500,500:x:screen1]", 3000),
      {
        fixture: "commit",
        model: "a",
        pointed: false,
        hit: false,
        latencyMs: 10,
        error: "rate limited",
      },
      scoreReply(fixture, "b", "[POINT:none]", 500),
    ]);
    expect(summary[0]).toMatchObject({
      model: "a",
      fixtures: 3,
      errors: 1,
      hitRate: 0.5,
      pointRate: 1,
      medianLatencyMs: 2000,
    });
    expect(summary[1]).toMatchObject({ model: "b", hitRate: 0, pointRate: 0 });
  });

  it("rejects a fixture whose target leaves its image", () => {
    expect(() =>
      validateFixture({ ...fixture, target: { x: 1180, y: 0, width: 20, height: 10 } }),
    ).toThrow();
    expect(() => validateFixture({ ...fixture, question: " " })).toThrow();
  });
});

describe("fixture capture", () => {
  it("captures at the size the desktop sends and keeps targets inside it", () => {
    expect(captureSize(1496, 967)).toEqual({ width: 1188, height: 768 });
    expect(captureSize(3440, 1440)).toEqual({ width: 1568, height: 656 });
    const size = captureSize(1496, 967);
    const scale = size.width / 1496;
    const box = targetBox(
      { role: "AXButton", name: "x", x: 1490, y: 960, width: 30, height: 30 },
      scale,
      size,
    );
    expect(box.x + box.width).toBeLessThanOrEqual(size.width);
    expect(box.y + box.height).toBeLessThanOrEqual(size.height);
  });
});
