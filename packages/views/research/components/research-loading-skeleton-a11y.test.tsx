// @vitest-environment jsdom

/**
 * LRM-1198 — [巡检][F] no-login static a11y contract for loading / skeleton shells.
 * Source scan + render asserts; mutually exclusive from LRM-1192 / 1196 / 1197.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ResearchSessionPageSkeleton } from "./research-session-page-skeleton";
import {
  ResearchSessionListSkeleton,
  ResearchSessionRowSkeleton,
} from "./research-session-row-skeleton";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM = /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

function readUiSkeletonSrc() {
  return fs.readFileSync(
    path.join(here, "..", "..", "..", "ui", "components", "ui", "skeleton.tsx"),
    "utf8",
  );
}

const SKELETON_FILES = [
  "research-session-page-skeleton.tsx",
  "research-session-row-skeleton.tsx",
] as const;

describe("research loading/skeleton a11y static contract (LRM-1198)", () => {
  it("bans sm structural visibility flips on research skeleton sources", () => {
    for (const file of SKELETON_FILES) {
      const src = readSrc(file);
      expect(src, file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: list-row time/people chrome uses md: (not sm:)", () => {
    const src = readSrc("research-session-row-skeleton.tsx");
    expect(src).toMatch(/\bmd:(?:block|flex)\b/);
    expect(src).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
  });

  it("source: shared Skeleton defaults decorative bars to aria-hidden", () => {
    const src = readUiSkeletonSrc();
    expect(src).toMatch(/aria-hidden=\{?(?:true|"true"|'true')\}?/);
  });

  it("source: page/list skeletons declare aria-busy", () => {
    expect(readSrc("research-session-page-skeleton.tsx")).toMatch(
      /aria-busy=["']true["']/,
    );
    expect(readSrc("research-session-row-skeleton.tsx")).toMatch(
      /aria-busy=["']true["']/,
    );
  });

  it("render: page skeleton root is busy + testid; bars are aria-hidden", () => {
    const { container } = render(<ResearchSessionPageSkeleton />);
    const root = container.querySelector(
      '[data-testid="research-session-page-skeleton"]',
    );
    expect(root).toBeTruthy();
    expect(root).toHaveAttribute("aria-busy", "true");
    const bars = [...(root?.querySelectorAll('[data-slot="skeleton"]') ?? [])];
    expect(bars.length).toBeGreaterThan(6);
    for (const bar of bars) {
      expect(bar).toHaveAttribute("aria-hidden", "true");
    }
  });

  it("render: list skeleton busy + optional aria-label; bars aria-hidden", () => {
    const { container, rerender } = render(
      <ResearchSessionListSkeleton rows={3} label="Loading sessions" />,
    );
    const root = container.querySelector(
      '[data-testid="research-session-list-skeleton"]',
    );
    expect(root).toBeTruthy();
    expect(root).toHaveAttribute("aria-busy", "true");
    expect(root).toHaveAttribute("aria-label", "Loading sessions");
    const bars = [...(root?.querySelectorAll('[data-slot="skeleton"]') ?? [])];
    expect(bars.length).toBeGreaterThan(3);
    for (const bar of bars) {
      expect(bar).toHaveAttribute("aria-hidden", "true");
    }

    rerender(<ResearchSessionListSkeleton rows={2} />);
    const unlabeled = container.querySelector(
      '[data-testid="research-session-list-skeleton"]',
    );
    expect(unlabeled).toHaveAttribute("aria-busy", "true");
    expect(unlabeled?.hasAttribute("aria-label")).toBe(false);
  });

  it("render: row skeleton exposes decorative bars only", () => {
    const { container } = render(<ResearchSessionRowSkeleton />);
    const root = container.querySelector(
      '[data-testid="research-session-row-skeleton"]',
    );
    expect(root).toBeTruthy();
    const bars = [...(root?.querySelectorAll('[data-slot="skeleton"]') ?? [])];
    expect(bars.length).toBeGreaterThan(2);
    for (const bar of bars) {
      expect(bar).toHaveAttribute("aria-hidden", "true");
    }
  });
});
