// @vitest-environment jsdom

/**
 * LRM-1109 — research breakpoint unify: useIsMobile (768) + Tailwind md:
 * must agree; 640–767 is no longer a dead zone where JS mobile coexists with
 * sm: desktop CSS.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { fireEvent, render, screen } from "@testing-library/react";
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ResearchReport, ResearchSession, ResearchSource } from "@multica/core/types";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import enResearch from "../../locales/en/research.json";
import { ReportReader } from "../report/report-reader";
import { ResearchAuxDrawer } from "./research-aux-drawer";
import { ResearchChatDrawer } from "./research-chat-drawer";
import { ResearchSessionRow } from "./research-session-row";
import { ResearchSessionRowSkeleton } from "./research-session-row-skeleton";
import { SourceStrategyStrip } from "./source-strategy-strip";

/** LRM-1164 / LRM-1179 — structural visibility flips must use md:, never sm:.
 * Exact tokens only — do not match sm:flex-row / sm:flex-1 (P3 typography layout). */
const FORBIDDEN_STRUCTURAL_SM = /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const originalWidth = window.innerWidth;
const originalMatchMedia = window.matchMedia;

function setViewport(px: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: px,
    writable: true,
  });
  window.matchMedia = ((query: string) => {
    const max = /max-width:\s*(\d+)px/.exec(query);
    const min = /min-width:\s*(\d+)px/.exec(query);
    let matches = false;
    if (max) matches = px <= Number(max[1]);
    else if (min) matches = px >= Number(min[1]);
    return {
      matches,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    } as MediaQueryList;
  }) as typeof window.matchMedia;
}

afterEach(() => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: originalWidth,
    writable: true,
  });
  window.matchMedia = originalMatchMedia;
});

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = fn(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
    i18n: { language: "en" },
  }),
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: React.ReactNode;
  }) => (open ? <div data-testid="sheet-root">{children}</div> : null),
  SheetContent: ({
    children,
    className,
    ...rest
  }: {
    children?: React.ReactNode;
    className?: string;
    "data-testid"?: string;
    "data-placement"?: string;
    "data-panel"?: string;
  }) => (
    <div
      data-testid={rest["data-testid"]}
      data-placement={rest["data-placement"]}
      data-panel={rest["data-panel"]}
      className={className}
    >
      {children}
    </div>
  ),
  SheetHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children?: React.ReactNode }) => <h2>{children}</h2>,
  SheetDescription: ({ children }: { children?: React.ReactNode }) => (
    <p>{children}</p>
  ),
}));

vi.mock("../../common/markdown", () => ({
  Markdown: ({ children }: { children: string }) => <div data-testid="md">{children}</div>,
}));

vi.mock("../../i18n/time", () => ({
  Time: ({ value, className }: { value: string; className?: string }) => (
    <time data-testid="list-time" className={className}>
      list:{value}
    </time>
  ),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    children,
    href,
    className,
    onClick,
  }: {
    children: React.ReactNode;
    href: string;
    className?: string;
    onClick?: () => void;
  }) => (
    <a href={href} className={className} onClick={onClick}>
      {children}
    </a>
  ),
}));

vi.mock("../../agents/components/agent-avatar-stack", () => ({
  AgentAvatarStack: ({ className }: { className?: string }) => (
    <span data-testid="avatar-stack" className={className} />
  ),
}));

vi.mock("./research-session-row-actions", () => ({
  ResearchSessionRowActions: () => <span data-testid="row-actions" />,
}));

const sampleReport: ResearchReport = {
  id: "r1",
  session_id: "s1",
  revision: 1,
  content_md: "## Findings\n\nBody.",
  structured: {},
  created_at: "",
  updated_at: "",
};

const sampleSources: ResearchSource[] = [
  {
    id: "src1",
    session_id: "s1",
    url: "https://example.com",
    title: "Example",
    source_class: "docs",
    credibility_weight: 0.9,
    stance: "neutral",
    relevance: 0.9,
    summary: "Summary",
    excerpt: "",
    payload: {},
    created_at: "",
    updated_at: "",
  },
];

function sampleSession(): ResearchSession {
  return {
    id: "s1",
    workspace_id: "workspace-1",
    fleet_id: "fleet-1",
    created_by: "user-1",
    title: "Alpha market map",
    goal: "Map the alpha market",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T03:00:00Z",
    fleet_preview: [{ agent_id: "agent-1", display_name: "Ronaldo", is_lead: true }],
  };
}

describe("LRM-1109 research breakpoint unify (matchMedia 360/700/768)", () => {
  describe("useIsMobile matchMedia tiers", () => {
    it.each([
      [360, true],
      [700, true],
      [767, true],
      [768, false],
    ] as const)("width %i → isMobile=%s", (width, expected) => {
      setViewport(width);
      const { result } = renderHook(() => useIsMobile());
      expect(result.current).toBe(expected);
    });
  });

  describe("JS layout branch follows the same tiers", () => {
    beforeEach(() => {
      setViewport(1024);
    });

    it.each([
      [360, "sheet"],
      [700, "sheet"],
      [767, "sheet"],
      [768, "float"],
    ] as const)("chat drawer at %ipx → %s", (width, placement) => {
      setViewport(width);
      render(
        <ResearchChatDrawer open onClose={() => {}}>
          <span>body</span>
        </ResearchChatDrawer>,
      );
      expect(screen.getByTestId("research-chat-drawer")).toHaveAttribute(
        "data-placement",
        placement,
      );
    });

    it("aux drawer stays full-bleed in the 640–767 dead zone (no sm:max-w-*)", () => {
      setViewport(700);
      render(
        <ResearchAuxDrawer panel="sources" onClose={() => {}}>
          <span>body</span>
        </ResearchAuxDrawer>,
      );
      const el = screen.getByTestId("research-aux-drawer");
      // LRM-1118 SoT: isMobile branch must not carry sm:* layout flips.
      expect(el.className).not.toMatch(/\bsm:max-w-/);
      expect(el.className).toMatch(/!max-w-none/);
    });
  });

  describe("CSS companions use md: (768), not sm: (640)", () => {
    it("LRM-1164 / LRM-1179: report outline and list-row structural companions wait until md", () => {
      const researchRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
      const read = (relativePath: string) =>
        fs.readFileSync(path.join(researchRoot, relativePath), "utf8");
      const [reader, listPage, row, skeleton] = [
        read("report/report-reader.tsx"),
        read("components/research-list-page.tsx"),
        read("components/research-session-row.tsx"),
        read("components/research-session-row-skeleton.tsx"),
      ];

      for (const src of [reader, listPage, row, skeleton]) {
        expect(src).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
      }

      expect(reader).toMatch(
        /className="md:hidden"\s+data-testid="research-report-outline-toggle"/,
      );
      expect(reader).toMatch(
        /data-testid="research-report-outline-drawer"\s+className="[^"]*\bmd:hidden"/,
      );
      expect(reader).toMatch(
        /data-testid="research-report-outline-aside"\s+className="[^"]*\bmd:block"/,
      );
      expect(listPage).toMatch(/className="hidden text-xs text-muted-foreground md:block"/);
      // LRM-1106: no goal chip; LRM-1285: desktop stage/time use fixed md:block slots
      // (md:inline alone shrink-to-fits and drifts with title length).
      expect(row).toMatch(/data-testid="research-session-stage-energy-slot"/);
      expect(row).toMatch(
        /className="relative z-\[1\] hidden w-28 shrink-0 md:block"/,
      );
      expect(row).toMatch(
        /className="hidden w-10 shrink-0 text-xs tabular-nums text-muted-foreground md:block"/,
      );
      expect(row).toMatch(/className="hidden shrink-0 md:flex"/);
      expect(row).toMatch(
        /className="[^"]*shrink-0 opacity-100 md:opacity-0 md:transition-opacity/,
      );
      expect(row).toMatch(/className="inline-flex [^"]*\bmd:hidden"/);
      expect(skeleton).toMatch(/className="hidden items-center md:flex"/);
      expect(skeleton).toMatch(/className="hidden h-6 w-28 shrink-0 md:block"/);
      expect(skeleton).toMatch(/className="hidden h-3 w-10 shrink-0 md:block"/);
    });

    it.each([360, 700, 767, 768] as const)(
      "LRM-1179: report/list-row/skeleton md: layout nodes render at %ipx",
      (width) => {
        setViewport(width);

        const { unmount: unmountSkeleton } = render(<ResearchSessionRowSkeleton />);
        const skeleton = screen.getByTestId("research-session-row-skeleton");
        expect(skeleton.querySelector(".hidden.h-6.w-28.shrink-0.md\\:block")).toBeTruthy();
        expect(skeleton.querySelector(".hidden.h-3.w-10.shrink-0.md\\:block")).toBeTruthy();
        expect(skeleton.querySelector(".hidden.items-center.md\\:flex")).toBeTruthy();
        expect(skeleton.className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        expect(skeleton.innerHTML).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        unmountSkeleton();

        const { unmount: unmountRow } = render(
          <ResearchSessionRow session={sampleSession()} href="/research/s1" />,
        );
        const narrowMeta = screen
          .getAllByTestId("research-session-stage-energy")
          .map((el) => el.parentElement)
          .find((el) => el?.className.includes("md:hidden"));
        expect(narrowMeta?.className).toMatch(/\bmd:hidden\b/);
        expect(narrowMeta?.className).toMatch(/\binline-flex\b/);
        expect(narrowMeta?.className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        const desktopStage = screen.getByTestId(
          "research-session-stage-energy-slot",
        );
        expect(desktopStage.className).toMatch(/\bhidden\b/);
        expect(desktopStage.className).toMatch(/\bw-28\b/);
        expect(desktopStage.className).toMatch(/\bmd:block\b/);
        expect(desktopStage.className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        expect(screen.getByTestId("avatar-stack").className).toMatch(/\bhidden\b/);
        expect(screen.getByTestId("avatar-stack").className).toMatch(/\bmd:flex\b/);
        expect(screen.getByTestId("avatar-stack").className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        unmountRow();

        const { unmount: unmountReader } = render(
          <ReportReader open onClose={() => {}} report={sampleReport} sources={sampleSources} />,
        );
        const toggle = screen.getByTestId("research-report-outline-toggle");
        expect(toggle.className).toMatch(/\bmd:hidden\b/);
        expect(toggle.className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        const aside = screen.getByTestId("research-report-outline-aside");
        expect(aside.className).toMatch(/\bhidden\b/);
        expect(aside.className).toMatch(/\bmd:block\b/);
        expect(aside.className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        fireEvent.click(toggle);
        const drawer = screen.getByTestId("research-report-outline-drawer");
        expect(drawer.className).toMatch(/\bmd:hidden\b/);
        expect(drawer.className).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
        unmountReader();
      },
    );

    it("source strategy strip uses auto-fit cards (360 drawer → one col; no sm:grid)", () => {
      setViewport(700);
      render(
        <SourceStrategyStrip
          model={{
            chips: [
              {
                id: "a",
                label: "A",
                layer: "general",
                samples: [],
                why: "why",
              },
            ],
            whyLine: "why",
            empty: false,
          }}
          sessionStatus="running"
        />,
      );
      const cards = screen.getByTestId("source-strategy-cards");
      expect(cards.className).toMatch(/minmax\(15rem,1fr\)/);
      expect(cards.className).not.toMatch(/\bsm:grid-cols/);
      expect(cards.className).not.toMatch(/md:grid-cols-3/);
    });
  });
});
