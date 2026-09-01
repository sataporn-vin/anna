import { describe, expect, it } from "vitest";

import { mergeCategoryRows } from "./annaClient.js";

describe("mergeCategoryRows", () => {
  it("combines nested paths under their top-level category", () => {
    const result = mergeCategoryRows([
      { _id: ["Food", "Restaurant"], totalMinor: 45_000, count: 2 },
      { _id: ["Transport", "Taxi"], totalMinor: 12_000, count: 1 },
      { _id: ["Food", "Groceries"], totalMinor: 30_000, count: 3 },
      { _id: [], totalMinor: 500, count: 1 },
    ]);

    expect(result).toEqual([
      { category: "Food", totalMinor: 75_000, count: 5 },
      { category: "Transport", totalMinor: 12_000, count: 1 },
      { category: "Uncategorized", totalMinor: 500, count: 1 },
    ]);
  });
});
