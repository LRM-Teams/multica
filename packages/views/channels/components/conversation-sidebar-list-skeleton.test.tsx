// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  ChannelListSkeleton,
  DmListSkeleton,
  InitialChannelsShellSkeleton,
} from "./conversation-sidebar-list-skeleton";

describe("conversation-sidebar-list-skeleton (LRM-459)", () => {
  it("renders DM row skeletons with busy semantics", () => {
    render(<DmListSkeleton rows={2} />);
    const root = screen.getByTestId("dm-list-skeleton");
    expect(root).toHaveAttribute("aria-busy", "true");
    expect(root.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(2);
  });

  it("renders channel row skeletons with busy semantics", () => {
    render(<ChannelListSkeleton rows={3} />);
    const root = screen.getByTestId("channel-list-skeleton");
    expect(root).toHaveAttribute("aria-busy", "true");
    expect(root.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(2);
  });

  it("initial shell paints list chrome (mobile-visible) plus desktop detail", () => {
    render(<InitialChannelsShellSkeleton />);
    const shell = screen.getByTestId("channels-initial-shell-skeleton");
    expect(shell).toBeInTheDocument();
    expect(screen.getByTestId("dm-list-skeleton")).toBeInTheDocument();
    expect(screen.getByTestId("channel-list-skeleton")).toBeInTheDocument();
    // Skeleton aside matches live listPane surface plane
    const aside = shell.querySelector("aside");
    expect(aside?.className).toMatch(/\bbg-background\b/);
    expect(aside?.className).not.toMatch(/\bbg-sidebar\b/);
    expect(aside?.className).toMatch(/\bborder-border\b/);
  });
});
