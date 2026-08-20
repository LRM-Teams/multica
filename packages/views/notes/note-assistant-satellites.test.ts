import { describe, expect, it } from "vitest";
import {
  NOTE_ASSISTANT_SATELLITE_IDS,
  noteAssistantSatelliteOffset,
} from "./note-assistant-satellites";

describe("noteAssistantSatelliteOffset", () => {
  it("places three actions on an upper-left arc", () => {
    const positions = NOTE_ASSISTANT_SATELLITE_IDS.map((_, index) =>
      noteAssistantSatelliteOffset(index, NOTE_ASSISTANT_SATELLITE_IDS.length),
    );

    expect(positions).toHaveLength(3);
    // Highest item is first; furthest-left item is last.
    expect(positions[0]!.y).toBeLessThan(positions[1]!.y);
    expect(positions[1]!.y).toBeLessThan(positions[2]!.y);
    expect(positions[2]!.x).toBeLessThan(positions[1]!.x);
    expect(positions[1]!.x).toBeLessThan(positions[0]!.x);
    const gap01 = Math.hypot(positions[1]!.x - positions[0]!.x, positions[1]!.y - positions[0]!.y);
    const gap12 = Math.hypot(positions[2]!.x - positions[1]!.x, positions[2]!.y - positions[1]!.y);
    expect(gap01).toBeGreaterThan(40);
    expect(gap12).toBeGreaterThan(40);
    for (const pos of positions) {
      expect(Math.hypot(pos.x, pos.y)).toBeLessThanOrEqual(44.01);
    }
  });

  it("centers a single satellite in the middle of the arc", () => {
    const only = noteAssistantSatelliteOffset(0, 1, 100);
    const mid = noteAssistantSatelliteOffset(1, 3, 100);
    expect(only.x).toBeCloseTo(mid.x, 5);
    expect(only.y).toBeCloseTo(mid.y, 5);
  });
});
