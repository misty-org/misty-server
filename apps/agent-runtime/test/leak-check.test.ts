import { describe, expect, it } from "vitest";
import { leftBehind } from "../evals/odysseys/leak-check.js";

describe("the leak check", () => {
  it("counts page processes started during a pass that no open view uses", () => {
    const before = { all: [10, 11], used: [10, 11] };
    const after = { all: [10, 11, 20, 21, 22], used: [10, 20] };
    expect(leftBehind(before, after)).toEqual([21, 22]);
  });
});
