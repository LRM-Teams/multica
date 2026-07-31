import { describe, expect, it } from "vitest";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";

describe("honor name display", () => {
  it("combines the unlocked style with the level luminosity tier", () => {
    const display = honorNameDisplayProps({
      nameStyle: "animated_glow",
      level: 50,
      surface: "profile",
    });

    expect(display.className).toContain("honor-name--animated-glow");
    expect(display.className).toContain("honor-name-glow");
    expect(display["data-honor-glow-tier"]).toBe("7");
    expect(display["data-honor-surface"]).toBe("profile");
    expect(display.style).toMatchObject({
      "--honor-pulse-duration": "4.8s",
    });
  });

  it("keeps compact surfaces progressive without using the full profile tier", () => {
    const display = honorNameDisplayProps({
      nameStyle: "animated_prismatic",
      level: 50,
      surface: "inline",
    });

    expect(display.className).toContain("honor-name--prismatic");
    expect(display.className).not.toContain("honor-name--animated_prismatic");
    expect(display["data-honor-glow-tier"]).toBe("5");
    expect(display["data-honor-surface"]).toBe("inline");
  });
});
