import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { ResearchSessionPageSkeleton } from "./research-session-page-skeleton";

describe("ResearchSessionPageSkeleton (LRM-781)", () => {
  it("mirrors chrome + timeline + canvas/chat shell instead of two flat bars", () => {
    const { container } = render(<ResearchSessionPageSkeleton />);
    const root = container.querySelector('[data-testid="research-session-page-skeleton"]');
    expect(root).toBeTruthy();
    expect(root).toHaveAttribute("aria-busy", "true");
    expect(root?.querySelector("header")).toBeTruthy();
    expect(root?.querySelector("nav")).toBeTruthy();
    // Canvas cards + chat drawer shell on sm+
    expect(root?.querySelectorAll("aside").length).toBeGreaterThanOrEqual(1);
    const bars = [...(root?.querySelectorAll('[data-slot="skeleton"]') ?? [])];
    expect(bars.length).toBeGreaterThan(6);
    // Must not be the old h-16 + flex-1 two-bar layout.
    expect(bars.every((el) => el.className.includes("h-16") && el.className.includes("w-full"))).toBe(
      false,
    );
  });
});
