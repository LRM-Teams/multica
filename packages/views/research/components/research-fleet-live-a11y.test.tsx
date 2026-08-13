// @vitest-environment jsdom

/**
 * LRM-1203 — [巡检][F] no-login static a11y contract for fleet strip + live stream.
 * LRM-1230 — fleet mode live region must persist across loading→running (chip
 * hosts native <output>; loading body keeps aria-busy only).
 * Source scan + render asserts; no authenticated routes.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ResearchFleetMember } from "@multica/core/types";
import { ResearchFleetStrip } from "./research-fleet-strip";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM = /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

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
        panel: {
          fleet: "Fleet",
          fleet_count: "{{count}} agents",
          fleet_loading_body: "Assembling fleet…",
          fleet_empty_title: "No agents yet",
          fleet_empty_body: "Wake agents to fill the strip.",
          fleet_done_hint: "Finished",
          fleet_mode: {
            empty: "Empty",
            loading: "Loading",
            running: "Running",
            done: "Done",
          },
          fleet_badge: {
            lead: "Lead",
            pending: "Pending",
            done: "Done",
          },
        },
        chat: {
          streaming_from: "Streaming from agent",
          streaming: "Generating…",
          stream_settled: "Settled",
          streaming_wait: "Waiting for tokens…",
        },
      });
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="mock-actor-avatar" />,
}));

vi.mock("../../channels/components/agent-compact-activity", () => ({
  AgentCompactActivity: () => <span data-testid="mock-activity">busy</span>,
}));

const TARGET_FILES = ["research-fleet-strip.tsx", "research-live-stream.tsx"] as const;

describe("research fleet/live a11y static contract (LRM-1203)", () => {
  it("bans sm structural visibility flips on fleet/live sources", () => {
    for (const file of TARGET_FILES) {
      expect(readSrc(file), file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: fleet hosts persistent OUTPUT live region on mode chip; loading body keeps busy only", () => {
    const src = readSrc("research-fleet-strip.tsx");
    expect(src).toMatch(
      /data-testid=["']research-fleet-strip-mode-live["'][\s\S]{0,200}aria-live=["']polite["']/,
    );
    expect(src).toMatch(
      /<output\b[\s\S]{0,160}data-testid=["']research-fleet-strip-mode-live["']|data-testid=["']research-fleet-strip-mode-live["'][\s\S]{0,80}>/,
    );
    expect(src).toMatch(
      /data-testid=["']research-fleet-strip-loading["'][\s\S]{0,200}aria-busy/,
    );
    // Announcement must not live on the loading-only subtree (unmounted on ready).
    expect(src).not.toMatch(
      /data-testid=["']research-fleet-strip-loading["'][\s\S]{0,240}aria-live/,
    );
    // Outer shell must not permanently announce.
    expect(src).not.toMatch(
      /data-testid=["']research-fleet-strip["'][^>]{0,200}aria-live/,
    );
  });

  it("source: fleet decorative Users icon is aria-hidden", () => {
    const src = readSrc("research-fleet-strip.tsx");
    expect(src).toMatch(/<Users\b[\s\S]{0,80}aria-hidden/);
  });

  it("source: live-stream article uses aria-busy while generating; no aria-live (LRM-1341)", () => {
    const src = readSrc("research-live-stream.tsx");
    expect(src).toMatch(/data-testid=["']research-live-stream["']/);
    // Drawer already hosts the persistent live region (LRM-1225); this card must not
    // re-announce stream tokens. Busy flag only while generating.
    expect(src).toMatch(/aria-busy=\{isGenerating\s*\|\|\s*undefined\}/);
    expect(src).not.toMatch(/aria-live/);
    expect(src).toMatch(/animate-pulse[\s\S]{0,120}aria-hidden/);
    expect(src).toMatch(/streaming_from/);
  });

  it("render: fleet keeps one persistent live region across mode flips", () => {
    const { rerender } = render(
      <ResearchFleetStrip members={[]} sessionStatus="running" loading />,
    );
    const live = screen.getByTestId("research-fleet-strip-mode-live");
    // Native <output> carries the status role (react-doctor prefer-tag-over-role).
    expect(live.tagName).toBe("OUTPUT");
    expect(live).toHaveAttribute("aria-live", "polite");
    expect(live).toHaveAttribute("aria-busy", "true");
    expect(live).toHaveTextContent("Loading");
    expect(screen.getByTestId("research-fleet-strip").getAttribute("aria-live")).toBeNull();
    expect(screen.getByTestId("research-fleet-strip-mode")).toHaveTextContent(
      "Loading",
    );
    const loading = screen.getByTestId("research-fleet-strip-loading");
    expect(loading.getAttribute("aria-busy")).toBe("true");
    expect(loading.getAttribute("aria-live")).toBeNull();

    const member: ResearchFleetMember = {
      id: "fm-1",
      agent_id: "ag-1",
      name: "Scout",
      display_name: "Scout",
      role: "researcher",
      status: "active",
      is_lead: true,
    };
    rerender(
      <ResearchFleetStrip members={[member]} sessionStatus="running" />,
    );
    // Same DOM node — a remount would reset the live region and swallow the
    // ready announcement.
    expect(screen.getByTestId("research-fleet-strip-mode-live")).toBe(live);
    expect(live).toHaveAttribute("aria-busy", "false");
    expect(live).toHaveTextContent("Running");
  });

  it("render: fleet empty/running keep mode text; no root aria-live", () => {
    const { unmount } = render(<ResearchFleetStrip members={[]} />);
    expect(screen.getByTestId("research-fleet-strip").getAttribute("aria-live")).toBeNull();
    expect(screen.getByTestId("research-fleet-strip-mode")).toHaveTextContent("Empty");
    expect(screen.getByTestId("research-fleet-strip-mode-live")).toHaveTextContent("Empty");
    expect(screen.getByTestId("research-fleet-strip-empty")).toBeTruthy();
    unmount();

    const member: ResearchFleetMember = {
      id: "fm-1",
      agent_id: "ag-1",
      name: "Scout",
      display_name: "Scout",
      role: "researcher",
      status: "active",
      is_lead: true,
    };

    render(
      <ResearchFleetStrip
        members={[member]}
        sessionStatus="running"
      />,
    );
    expect(screen.getByTestId("research-fleet-strip").getAttribute("aria-live")).toBeNull();
    expect(screen.getByTestId("research-fleet-strip-mode")).toHaveTextContent("Running");
    expect(screen.getByText("Scout")).toBeTruthy();
  });
});
