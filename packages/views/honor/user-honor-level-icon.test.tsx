import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  MAX_USER_HONOR_LEVEL,
  UserHonorLevelIcon,
  normalizeUserHonorLevel,
  userHonorLevelIconURL,
} from "./user-honor-level-icon";

describe("UserHonorLevelIcon", () => {
  it("publishes the approved icon for every user honor level", () => {
    expect(MAX_USER_HONOR_LEVEL).toBe(80);
    expect(userHonorLevelIconURL(1)).toBe(
      "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-01.webp",
    );
    expect(userHonorLevelIconURL(40)).toBe(
      "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-40.webp",
    );
    expect(userHonorLevelIconURL(41)).toBe(
      "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-41.webp",
    );
    expect(userHonorLevelIconURL(80)).toBe(
      "https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-80.webp",
    );
  });

  it("clamps stale or invalid server levels to the available asset range", () => {
    expect(normalizeUserHonorLevel(0)).toBe(1);
    expect(normalizeUserHonorLevel(42.9)).toBe(42);
    expect(normalizeUserHonorLevel(81)).toBe(80);
    expect(normalizeUserHonorLevel(Number.NaN)).toBe(1);
  });

  it("renders a sized decorative image by default", () => {
    const { container } = render(<UserHonorLevelIcon level={42} />);

    const icon = container.querySelector("img");
    expect(icon).not.toBeNull();
    expect(icon).toHaveAttribute("width", "256");
    expect(icon).toHaveAttribute("height", "256");
    expect(icon).toHaveAttribute("data-user-honor-level", "42");
  });
});
