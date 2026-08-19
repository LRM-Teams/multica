import { describe, expect, it } from "vitest";
import {
  BOTLAB_BODIES,
  BOTLAB_EYES,
  BOTLAB_HEADS,
  BOTLAB_MOUTHS,
  BOTLAB_PALETTES,
  BOTLAB_TOPS,
  composeBotlabGrid,
  randomBotlabAvatar,
  renderBotlabAvatarRgba,
} from "./botlab-avatar";

describe("randomBotlabAvatar", () => {
  it("maps a zero draw onto the first part of every slot", () => {
    expect(randomBotlabAvatar(() => 0)).toEqual({
      body: 0,
      head: 0,
      eyes: 0,
      mouth: 0,
      top: 0,
      palette: 0,
    });
  });

  it("maps a near-one draw onto the last part of every slot", () => {
    expect(randomBotlabAvatar(() => 0.999)).toEqual({
      body: BOTLAB_BODIES.length - 1,
      head: BOTLAB_HEADS.length - 1,
      eyes: BOTLAB_EYES.length - 1,
      mouth: BOTLAB_MOUTHS.length - 1,
      top: BOTLAB_TOPS.length - 1,
      palette: BOTLAB_PALETTES.length - 1,
    });
  });
});

describe("composeBotlabGrid", () => {
  it("composites body, head, eyes, mouth, and top for the factory cube worker", () => {
    const grid = composeBotlabGrid({
      body: 0,
      head: 0,
      eyes: 0,
      mouth: 0,
      top: 0,
      palette: 0,
    });

    expect(grid).toHaveLength(16);
    expect(grid.every((row) => row.length === 16)).toBe(true);
    expect(grid[0]?.[7]).toBe("e");
    expect(grid[5]?.[6]).toBe("o");
    expect(grid[7]?.[6]).toBe("o");
    expect(grid[9]?.[5]).toBe("a");
  });
});

describe("renderBotlabAvatarRgba", () => {
  it("paints a 512² nearest-neighbor sprite with a transparent cell", () => {
    const rgba = renderBotlabAvatarRgba({
      body: 0,
      head: 0,
      eyes: 0,
      mouth: 0,
      top: 0,
      palette: 0,
    });
    expect(rgba.length).toBe(512 * 512 * 4);
    expect(rgba[0]).toBe(0);
    expect(rgba[1]).toBe(0);
    expect(rgba[2]).toBe(0);
    expect(rgba[3]).toBe(0);

    const antenna = (7 * 32) * 4;
    expect(Array.from(rgba.slice(antenna, antenna + 4))).toEqual([255, 210, 62, 255]);
  });
});
