import { describe, expect, it } from "vitest";
import {
  HONOR_HERO_IMAGE_FALLBACK_URL,
  HONOR_HERO_IMAGE_URL,
  honorAssetFallbackURL,
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
    expect(honorAssetFallbackURL("users/user-honor-level-01.webp")).toBe(
      "/honor-assets/v1/users/user-honor-level-01.webp",
    );
    expect(HONOR_HERO_IMAGE_FALLBACK_URL).toBe(
      "/honor-assets/v1/honor-center-orbit.webp",
    );
  });
});
