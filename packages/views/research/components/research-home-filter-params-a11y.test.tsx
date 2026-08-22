// @vitest-environment jsdom

/**
 * LRM-1207 — [巡检][F] no-login static a11y for home hero + filter + params summary.
 * Source scan + render asserts; no authenticated routes.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchHomeHero } from "./research-home-hero";
import { ResearchSessionFilterBar } from "./research-session-filter-bar";
import { ResearchSessionParamsSummary } from "./research-session-params-summary";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const raw = fn({
        home: {
          composer_label: "Research composer",
          frame_title: "New run",
          frame_tag: "NEW RUN",
          hero_desc: "Start from a goal.",
        },
        filter: {
          search_placeholder: "Search sessions",
          search_label: "Search research sessions",
          status_label: "Session status",
          status_all: "All",
          status_in_progress: "In progress",
          status_completed: "Completed",
          status_failed: "Failed",
          count_aria: "{{label}}, {{count}}",
        },
        create_params: {
          session_hint: "Create-time params for this session.",
          depth_label: "Depth",
          language_label: "Language",
          weights_label: "Source weights",
          depth_tiers: {
            shallow: { label: "Shallow" },
            standard: { label: "Standard" },
            deep: { label: "Deep" },
          },
          language_options: { zh: "Chinese", en: "English" },
          weight_rows: {
            primary: { label: "Primary" },
            secondary: { label: "Secondary" },
            community: { label: "Community" },
          },
        },
      });
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(vars[key] ?? ""),
      );
    },
    i18n: { language: "en" },
  }),
}));

const TARGET_FILES = [
  "research-home-hero.tsx",
  "research-session-filter-bar.tsx",
  "research-session-params-summary.tsx",
] as const;

const EMPTY_COUNTS = {
  all: 3,
  in_progress: 1,
  completed: 2,
  failed: 0,
};

describe("research home/filter/params a11y static contract (LRM-1207)", () => {
  it("bans sm structural visibility flips on hero/filter/params sources", () => {
    for (const file of TARGET_FILES) {
      expect(readSrc(file), file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: hero section has accessible name; corner studs aria-hidden; visible h2", () => {
    const src = readSrc("research-home-hero.tsx");
    expect(src).toMatch(/data-testid=["']research-home-hero["']/);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.home\.composer_label\)\}/);
    expect(src).toMatch(/<PixelStuds\b/);
    expect(src).toMatch(/<h2\b/);
    const studs = readSrc("pixel-studs.tsx");
    expect(studs).toMatch(/aria-hidden/);
  });

  it("source: filter search labelled; Search icon hidden; radiogroup + aria-checked radios", () => {
    const src = readSrc("research-session-filter-bar.tsx");
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.filter\.search_label\)\}/);
    expect(src).toMatch(/<Search\b[\s\S]{0,240}aria-hidden/);
    expect(src).toMatch(/role=["']radiogroup["']/);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.filter\.status_label\)\}/);
    expect(src).toMatch(/role=["']radio["']/);
    expect(src).toMatch(/aria-checked=\{selected\}/);
  });

  it("source: params summary uses dl/dt/dd with depth/language/weights", () => {
    const src = readSrc("research-session-params-summary.tsx");
    expect(src).toMatch(/data-testid=["']research-session-params-summary["']/);
    expect(src).toMatch(/<dl\b/);
    expect(src).toMatch(/create_params\.depth_label/);
    expect(src).toMatch(/create_params\.language_label/);
    expect(src).toMatch(/create_params\.weights_label/);
  });

  it("render: hero exposes named region, hidden stud chrome, visible title", () => {
    render(<ResearchHomeHero />);
    const hero = screen.getByTestId("research-home-hero");
    expect(hero.tagName).toBe("SECTION");
    expect(
      screen.getByRole("region", { name: "Research composer" }),
    ).toBe(hero);
    expect(hero.querySelector("[aria-hidden]")).toBeTruthy();
    expect(
      screen.getByRole("heading", { level: 2, name: "New run" }),
    ).toBeTruthy();
  });

  it("render: filter search + status radios expose checked state", () => {
    render(
      <ResearchSessionFilterBar
        query=""
        status="in_progress"
        counts={EMPTY_COUNTS}
        onQueryChange={() => {}}
        onStatusChange={() => {}}
        onClear={() => {}}
      />,
    );
    expect(
      screen.getByRole("textbox", { name: "Search research sessions" }),
    ).toBeTruthy();
    const group = screen.getByRole("radiogroup", { name: "Session status" });
    expect(
      within(group).getByRole("radio", { name: "In progress, 1" }),
    ).toHaveAttribute("aria-checked", "true");
    expect(
      within(group).getByRole("radio", { name: "All, 3" }),
    ).toHaveAttribute("aria-checked", "false");
    expect(
      within(group).queryByTestId("research-filter-status-failed"),
    ).toBeNull();
  });

  it("render: params summary exposes readable dl terms and values", () => {
    render(
      <ResearchSessionParamsSummary
        session={{ goal: "Ship a11y", depth_tier: "standard" }}
        contract={{ language: "en", source_policy: {} }}
      />,
    );
    const root = screen.getByTestId("research-session-params-summary");
    expect(root.querySelector("dl")).toBeTruthy();
    expect(screen.getByText("Depth")).toBeTruthy();
    expect(screen.getByText("Standard")).toBeTruthy();
    expect(screen.getByText("Language")).toBeTruthy();
    expect(screen.getByText("English")).toBeTruthy();
    expect(screen.getByText("Source weights")).toBeTruthy();
    expect(screen.getByText("Primary")).toBeTruthy();
  });
});
