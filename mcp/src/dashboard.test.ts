import { describe, expect, it } from "vitest";

import { summarizeEnergy } from "./dashboard.js";

describe("summarizeEnergy", () => {
  it("builds daily totals and ranks positive and negative experiences", () => {
    const result = summarizeEnergy([
      { id: "1", occurredOn: "2026-08-29", summary: "Pilates", energyDelta: 2, context: ["fitness"] },
      { id: "2", occurredOn: "2026-08-29", summary: "Recovery", energyDelta: 1, context: ["recovery"] },
      { id: "3", occurredOn: "2026-08-31", summary: "Interrupted", energyDelta: -3, context: ["family"] },
    ]);

    expect(result.totalDelta).toBe(0);
    expect(result.averageDelta).toBe(0);
    expect(result.daily).toEqual([
      { occurredOn: "2026-08-29", totalDelta: 3, count: 2 },
      { occurredOn: "2026-08-31", totalDelta: -3, count: 1 },
    ]);
    expect(result.topPositive.map(({ id }) => id)).toEqual(["1", "2"]);
    expect(result.topNegative.map(({ id }) => id)).toEqual(["3"]);
  });
});
