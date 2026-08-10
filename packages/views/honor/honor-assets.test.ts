import { describe, expect, it } from "vitest";
import {
  HONOR_HERO_IMAGE_URL,
  honorAssetURL,
} from "./honor-assets";

describe("honor asset catalog", () => {
  it("uses immutable versioned CDN paths", () => {
    expect(honorAssetURL("users/user-honor-level-01.webp")).toBe(
      "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-01.webp",
    );
    expect(HONOR_HERO_IMAGE_URL).toBe(
      "https://cdn.leagent.me/honor-assets/v1/honor-center-orbit.webp",
    );
  });
});
