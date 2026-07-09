import { describe, expect, it } from "vitest";
import { computeNewArrivals } from "./use-new-arrivals-pill";

const authored = (...items: Array<[number, string]>) =>
  items.map(([seq, author_id]) => ({ id: `m${seq}`, seq, author_id }));

describe("computeNewArrivals", () => {
  it("returns null when the seen-through boundary is unknown", () => {
    expect(computeNewArrivals(authored([1, "o"]), null, "u1")).toBeNull();
  });

  it("returns null when nothing arrived past what you've seen", () => {
    expect(computeNewArrivals(authored([1, "o"], [2, "o"]), 2, "u1")).toBeNull();
  });

  it("counts others' messages past the boundary and reports the first id", () => {
    expect(
      computeNewArrivals(authored([1, "o"], [2, "o"], [3, "o"], [4, "o"]), 2, "u1"),
    ).toEqual({ count: 2, firstMessageId: "m3" });
  });

  it("excludes the viewer's own arrivals", () => {
    // past 2: m3 (other), m4 (own), m5 (other) → count 2, first m3
    const messages = authored([1, "o"], [2, "o"], [3, "o"], [4, "u1"], [5, "o"]);
    expect(computeNewArrivals(messages, 2, "u1")).toEqual({
      count: 2,
      firstMessageId: "m3",
    });
  });

  it("ignores the viewer id when null (counts everyone's arrivals)", () => {
    expect(computeNewArrivals(authored([1, "a"], [2, "b"]), 0, null)).toEqual({
      count: 2,
      firstMessageId: "m1",
    });
  });
});
