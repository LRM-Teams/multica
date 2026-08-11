/**
 * LRM-1117 — parallel regression smoke matrix (list / breakpoints / keyboard).
 *
 * Reuse:
 *   pnpm --filter @multica/views test:research-parallel-smoke
 *
 * Failure titles always include the owning Issue (LRM-1104 / 1109 / 1100 / 1105 / 1091).
 * Known open gaps use `it.fails` so CI stays green until the owning PR flips them to `it`.
 * Do not edit production components from this suite — avoids file conflicts with parallel knives.
 *
 * Gate status (dev @22d89477b+):
 * - 1100 overlay Esc/a11y — hard
 * - 1104 goal-chip dedupe (#1949) + no max-w-3xl shell (#1962) — hard
 * - 1105 helpers (#1952) + role=application (1091) + Home/End via resolveCanvasKeyEvent — hard
 * - 1109 meta-menu md: (#1947) — hard; template chip-row sm: still `it.fails`
 * - 1091 planar layout + arrow/Enter/Esc/F10 + retry status gate — hard;
 *   legacy planar session canvas removed (D5 star-graph is canonical).
 */
// @vitest-environment jsdom
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchGraphNode, ResearchSession } from "@multica/core/types";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  ACTION_VISIBILITY_CONTRACT,
  BRANCH_VS_STATUS_COLOR_CONTRACT,
  BREAKPOINT_SMOKE_WIDTHS,
  CANVAS_KEYBOARD_CONTRACT,
  MOBILE_BREAKPOINT_PX,
  OVERLAY_A11Y_CONTRACT,
  PLANAR_KEYBOARD_CONTRACT,
  SMOKE_ISSUES,
  failHint,
  findOverlappingPairs,
  isGoalChipRedundant,
  isMobileViewport,
  type SmokeRect,
} from "./contracts";
import {
  crossLaneNeighbor,
  isForkPoint,
  layoutResearchGraph,
  mainChainNeighbor,
  mainChainOrder,
  RESEARCH_NODE_HEIGHT,
  RESEARCH_NODE_WIDTH,
} from "../lib/layout-graph";
import {
  resolveCanvasKeyEvent,
  type CanvasKeyboardContext,
} from "../lib/canvas-keyboard-nav";
import { ringActionsForNode } from "../lib/node-action-ring";
import { visualForEdgeType, visualForNodeType } from "../lib/node-visuals";
import {
  sessionGoalSummary,
  sessionShortTitle,
} from "../lib/session-list-filter";
import enResearch from "../../locales/en/research.json";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const RESEARCH_ROOT = path.resolve(__dirname, "..");

/** D5 star-map canvas — canonical keyboard/a11y surface (replaces planar research-canvas). */
const D5_CANVAS_SOURCE = "star-graph/components/star-graph-canvas.tsx";

function readResearchSource(relPath: string): string {
  return fs.readFileSync(path.join(RESEARCH_ROOT, relPath), "utf8");
}

function setViewportWidth(widthPx: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    writable: true,
    value: widthPx,
  });
  window.matchMedia = (query: string) => {
    const maxMatch = /max-width:\s*(\d+)px/.exec(query);
    const minMatch = /min-width:\s*(\d+)px/.exec(query);
    let matches = false;
    if (maxMatch) matches = widthPx <= Number(maxMatch[1]);
    if (minMatch) matches = widthPx >= Number(minMatch[1]);
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
  };
}

function ProbeMobile() {
  const mobile = useIsMobile();
  return <span data-testid="mobile-probe">{String(mobile)}</span>;
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = fn(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
  }),
}));

vi.mock("../../i18n/use-time-ago", () => ({
  useTimeAgo: () => (dateStr: string) => `ago:${dateStr}`,
}));

vi.mock("../../i18n/time", () => ({
  Time: ({ value }: { value: string }) => <time data-testid="list-time">{value}</time>,
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    children,
    href,
    className,
  }: {
    children: React.ReactNode;
    href: string;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));

vi.mock("../../agents/components/agent-avatar-stack", () => ({
  AgentAvatarStack: () => <span data-testid="avatar-stack" />,
}));

vi.mock("../components/research-session-row-actions", () => ({
  ResearchSessionRowActions: () => <span data-testid="row-actions" />,
}));

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ open, children }: { open: boolean; children: React.ReactNode }) =>
    open ? <div data-testid="goal-dialog">{children}</div> : null,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
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
    ...rest
  }: {
    children?: React.ReactNode;
    "data-testid"?: string;
  }) => <div data-testid={rest["data-testid"]}>{children}</div>,
  SheetHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children?: React.ReactNode }) => <h2>{children}</h2>,
  SheetDescription: ({ children }: { children?: React.ReactNode }) => <p>{children}</p>,
}));

import { ResearchSessionRow } from "../components/research-session-row";
import { ResearchAuxDrawer } from "../components/research-aux-drawer";
import { ResearchChatDrawer } from "../components/research-chat-drawer";
import { ResearchNodeActionRing } from "../components/research-node-action-ring";

function session(partial: Partial<ResearchSession> = {}): ResearchSession {
  return {
    id: "s1",
    workspace_id: "workspace-1",
    fleet_id: "fleet-1",
    created_by: "user-1",
    title: "",
    goal: "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T03:00:00Z",
    fleet_preview: [],
    ...partial,
  };
}

describe(`Smoke · list duplicate info (${SMOKE_ISSUES.listDuplicate})`, () => {
  it(`${SMOKE_ISSUES.listDuplicate}: pure contract — equal / prefix goal is redundant`, () => {
    expect(isGoalChipRedundant("如何开发一个网页游戏", "如何开发一个网页游戏")).toBe(true);
    expect(isGoalChipRedundant("如何开发一个网页游戏…", "如何开发一个网页游戏")).toBe(true);
    expect(isGoalChipRedundant("如何开发一个网页游戏", "如何开发")).toBe(true);
    expect(
      isGoalChipRedundant("Alpha market map", "Map the alpha market across regions"),
    ).toBe(false);
  });

  it(`${SMOKE_ISSUES.listDuplicate}: empty-title row short-title and goal-summary share a stem today`, () => {
    const s = session({ title: "", goal: "如何开发一个网页游戏。对标游戏传奇网页版。" });
    const title = sessionShortTitle(s);
    const goal = sessionGoalSummary(s);
    expect(
      isGoalChipRedundant(title, goal),
      failHint(
        SMOKE_ISSUES.listDuplicate,
        `title="${title}" still duplicates goal chip "${goal}"`,
      ),
    ).toBe(true);
  });

  // LRM-1104 #1949/#1962 merged — chip dedupe + no max-w-3xl shell are hard gates.
  it(`${SMOKE_ISSUES.listDuplicate}: empty-title row must omit redundant goal chip`, () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const titleEl = document.querySelector(
      '[data-testid="research-session-row"] .font-medium.tracking-tight',
    );
    const chip = screen.queryByTestId("research-session-goal-chip");
    expect(
      chip,
      failHint(SMOKE_ISSUES.listDuplicate, "redundant goal chip still rendered"),
    ).toBeNull();
    expect(titleEl?.textContent?.length).toBeGreaterThan(0);
  });

  it(
    `${SMOKE_ISSUES.listDuplicate}: LRM-1106 D2 — inline goal chip never renders (goal lives in ⋯ menu)`,
    () => {
      const { unmount } = render(
        <ResearchSessionRow
          session={session({
            title: "如何开发一个网页游戏",
            goal: "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员",
          })}
          href="/research/s1"
        />,
      );
      expect(
        screen.queryByTestId("research-session-goal-chip"),
        failHint(SMOKE_ISSUES.listDuplicate, "prefix-redundant chip should be hidden"),
      ).toBeNull();
      unmount();

      render(
        <ResearchSessionRow
          session={session({
            title: "Alpha market map",
            goal: "Map the alpha market across regions with pricing and share",
          })}
          href="/research/s1"
        />,
      );
      expect(
        screen.queryByTestId("research-session-goal-chip"),
        failHint(SMOKE_ISSUES.listDuplicate, "distinct goal must not revive inline chip under D2"),
      ).toBeNull();
    },
  );

  // LRM-1104 #1962 + LRM-1106: no max-w-3xl shell; workbench owns max-w-[1240px].
  it(`${SMOKE_ISSUES.listDuplicate}: research list workbench has max-w-[1240px] and no max-w-3xl shell`, () => {
    const listSrc = stripComments(readResearchSource("components/research-list-page.tsx"));
    const filterSrc = stripComments(readResearchSource("components/research-session-filter-bar.tsx"));
    const heroSrc = stripComments(readResearchSource("components/research-home-hero.tsx"));
    expect(
      !/\bmax-w-3xl\b/.test(listSrc),
      failHint(SMOKE_ISSUES.listDuplicate, "research-list-page.tsx still has max-w-3xl shell"),
    ).toBe(true);
    expect(
      !/\bmax-w-3xl\b/.test(filterSrc),
      failHint(SMOKE_ISSUES.listDuplicate, "research-session-filter-bar.tsx still has max-w-3xl"),
    ).toBe(true);
    expect(
      !/\bmax-w-3xl\b/.test(heroSrc),
      failHint(SMOKE_ISSUES.listDuplicate, "hero still pins max-w-3xl"),
    ).toBe(true);
    expect(
      /\bmax-w-\[1240px\]\b/.test(listSrc) || listSrc.includes("RESEARCH_LIST_WORKBENCH"),
      failHint(SMOKE_ISSUES.listDuplicate, "list page missing workbench width"),
    ).toBe(true);
  });
});

/** Strip // and /* *\/ comments so inventory assertions ignore documentation mentions. */
function stripComments(source: string): string {
  return source.replace(/\/\/.*$/gm, "").replace(/\/\*[\s\S]*?\*\//g, "");
}

describe(`Smoke · breakpoints 360/700/767/768 (${SMOKE_ISSUES.breakpoints})`, () => {
  afterEach(() => {
    setViewportWidth(1024);
  });

  it(`${SMOKE_ISSUES.breakpoints}: useIsMobile matches <${MOBILE_BREAKPOINT_PX} at smoke widths`, () => {
    for (const width of BREAKPOINT_SMOKE_WIDTHS) {
      setViewportWidth(width);
      const { unmount } = render(<ProbeMobile />);
      expect(
        screen.getByTestId("mobile-probe").textContent,
        failHint(SMOKE_ISSUES.breakpoints, `width=${width} mobile flag`),
      ).toBe(String(isMobileViewport(width)));
      unmount();
    }
  });

  it(`${SMOKE_ISSUES.breakpoints}: 767 vs 768 flip exactly at the mobile boundary`, () => {
    expect(isMobileViewport(767)).toBe(true);
    expect(isMobileViewport(768)).toBe(false);
    expect(isMobileViewport(700)).toBe(true);
    expect(isMobileViewport(360)).toBe(true);
  });

  it(`${SMOKE_ISSUES.breakpoints}: baseline inventory — template chip-row uses md: (768)`, () => {
    // LRM-1109 #1947: meta-menu no longer pairs useIsMobile with sm: classes.
    const meta = readResearchSource("components/research-session-meta-menu.tsx");
    expect(meta.includes("useIsMobile")).toBe(true);
    expect(
      hasTailwindSmClass(meta),
      failHint(
        SMOKE_ISSUES.breakpoints,
        "meta-menu regressed: sm: class companion returned beside useIsMobile",
      ),
    ).toBe(false);

    // LRM-1106: composer chip row aligns with md / 768.
    const templates = readResearchSource("components/research-template-chip-row.tsx");
    expect(
      /\bmd:/.test(templates) && !hasTailwindSmClass(templates),
      failHint(
        SMOKE_ISSUES.breakpoints,
        "template-chip-row should switch layout at md: (768)",
      ),
    ).toBe(true);
  });

  it(`${SMOKE_ISSUES.breakpoints}: useIsMobile companions must use md: not sm: (meta-menu)`, () => {
    const meta = readResearchSource("components/research-session-meta-menu.tsx");
    expect(
      meta.includes("useIsMobile") && !hasTailwindSmClass(meta),
      failHint(
        SMOKE_ISSUES.breakpoints,
        "replace companion sm: with md: in research-session-meta-menu.tsx",
      ),
    ).toBe(true);
  });

  it(
    `${SMOKE_ISSUES.breakpoints}: template chip-row layout switch should be md: (align with 768)`,
    () => {
      const templates = readResearchSource("components/research-template-chip-row.tsx");
      expect(
        /\bmd:/.test(templates) && !hasTailwindSmClass(templates),
        failHint(
          SMOKE_ISSUES.breakpoints,
          "research-template-chip-row.tsx still uses sm: instead of md:",
        ),
      ).toBe(true);
    },
  );
});

/** Detect Tailwind `sm:` utility classes; ignore `sm:` mentioned only in comments. */
function hasTailwindSmClass(source: string): boolean {
  return /(^|[^a-zA-Z0-9_-])sm:/.test(stripComments(source));
}

describe(`Smoke · Esc / focus / keyboard (${SMOKE_ISSUES.overlayA11y} / ${SMOKE_ISSUES.canvasKeyboard})`, () => {
  beforeEach(() => {
    setViewportWidth(1280);
  });

  it(`${SMOKE_ISSUES.overlayA11y}: contract freeze — Esc / focus / names required on desktop overlays`, () => {
    expect(OVERLAY_A11Y_CONTRACT.escapeCloses).toBe(true);
    expect(OVERLAY_A11Y_CONTRACT.focusMovesInOnOpen).toBe(true);
    expect(OVERLAY_A11Y_CONTRACT.focusRestoresOnClose).toBe(true);
    expect(OVERLAY_A11Y_CONTRACT.auxHasAriaLabelledby).toBe(true);
    expect(OVERLAY_A11Y_CONTRACT.chatHasAriaLabel).toBe(true);
  });

  it(`${SMOKE_ISSUES.canvasKeyboard}: keyboard map freeze covers arrows / Enter / Esc / zoom / Home End`, () => {
    expect(CANVAS_KEYBOARD_CONTRACT.ArrowLeft).toBe("main-chain-prev");
    expect(CANVAS_KEYBOARD_CONTRACT.ArrowRight).toBe("main-chain-next");
    expect(CANVAS_KEYBOARD_CONTRACT.Enter).toBe("open-detail-drawer");
    expect(CANVAS_KEYBOARD_CONTRACT.Escape).toBe("dismiss-layer");
    expect(CANVAS_KEYBOARD_CONTRACT.Home).toBe("jump-first");
    expect(CANVAS_KEYBOARD_CONTRACT.End).toBe("jump-last");
    expect(Object.keys(CANVAS_KEYBOARD_CONTRACT).length).toBeGreaterThanOrEqual(12);
  });

  it(`${SMOKE_ISSUES.overlayA11y}: positive control — action ring closes on Escape`, () => {
    const onClose = vi.fn();
    const node = {
      id: "n1",
      session_id: "s1",
      title: "Blocked",
      summary: "x",
      status: "abandoned",
      node_type: "dead_end",
      actor_agent_id: null,
      payload: {},
      created_at: "2026-07-31T00:00:00Z",
      updated_at: "2026-07-31T00:00:00Z",
    } as unknown as ResearchGraphNode;
    render(
      <ResearchNodeActionRing
        node={node}
        mode="ring"
        onAction={vi.fn()}
        onClose={onClose}
      />,
    );
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  // LRM-1100 merged — these are hard gates now (was it.fails).
  it(`${SMOKE_ISSUES.overlayA11y}: desktop aux drawer closes on Escape`, () => {
    const onClose = vi.fn();
    render(
      <ResearchAuxDrawer panel="detail" onClose={onClose}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    expect(screen.getByTestId("research-aux-drawer").tagName.toLowerCase()).toBe("aside");
    fireEvent.keyDown(window, { key: "Escape" });
    expect(
      onClose,
      failHint(SMOKE_ISSUES.overlayA11y, "desktop aux drawer ignore Escape"),
    ).toHaveBeenCalled();
  });

  it(`${SMOKE_ISSUES.overlayA11y}: desktop aux drawer exposes aria-labelledby to visible title`, () => {
    render(
      <ResearchAuxDrawer panel="trajectory" onClose={() => {}}>
        <span>body</span>
      </ResearchAuxDrawer>,
    );
    const aside = screen.getByTestId("research-aux-drawer");
    const labelledBy = aside.getAttribute("aria-labelledby");
    expect(
      labelledBy,
      failHint(SMOKE_ISSUES.overlayA11y, "aux drawer missing aria-labelledby"),
    ).toBeTruthy();
    expect(document.getElementById(labelledBy!)).toBeTruthy();
  });

  it(`${SMOKE_ISSUES.overlayA11y}: desktop chat drawer has aria-label and closes on Escape`, () => {
    const onClose = vi.fn();
    render(
      <ResearchChatDrawer open onClose={onClose}>
        <span>body</span>
      </ResearchChatDrawer>,
    );
    const aside = screen.getByTestId("research-chat-drawer");
    expect(
      aside.getAttribute("aria-label"),
      failHint(SMOKE_ISSUES.overlayA11y, "chat drawer missing aria-label"),
    ).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(
      onClose,
      failHint(SMOKE_ISSUES.overlayA11y, "desktop chat drawer ignore Escape"),
    ).toHaveBeenCalled();
  });

  // LRM-1105 slice 1 (#1952) landed pure helpers — hard gates. Canvas wiring waits on 1091.
  it(`${SMOKE_ISSUES.canvasKeyboard}: layout-graph exports main-chain / fork-A helpers`, () => {
    const { nodes, edges } = keyboardForkFixture();
    expect(mainChainOrder(nodes, edges)[0]).toBe("goal");
    expect(mainChainNeighbor(nodes, edges, "goal", 1)).toBe("fork");
    expect(
      isForkPoint("fork", nodes, edges),
      failHint(SMOKE_ISSUES.canvasKeyboard, "fork fixture should be a fork point"),
    ).toBe(true);
    expect(
      isForkPoint("goal", nodes, edges),
      failHint(SMOKE_ISSUES.canvasKeyboard, "goal must not be a fork point"),
    ).toBe(false);
    expect(
      crossLaneNeighbor(nodes, edges, "a", 1),
      failHint(SMOKE_ISSUES.canvasKeyboard, "↑↓ must be null off fork (semantics A)"),
    ).toBeNull();
    expect(crossLaneNeighbor(nodes, edges, "fork", 1)).toBe("a");
  });

  // LRM-1091 planar canvas shipped role=application + name — hard gate now.
  it(
    `${SMOKE_ISSUES.canvasKeyboard}: canvas root declares role=application with accessible name (D5 star-graph)`,
    () => {
      const canvasSrc = readResearchSource(D5_CANVAS_SOURCE);
      expect(
        /role=["']application["']/.test(canvasSrc),
        failHint(SMOKE_ISSUES.canvasKeyboard, "star-graph-canvas.tsx missing role=application"),
      ).toBe(true);
      expect(
        /aria-label=/.test(canvasSrc) || /aria-labelledby=/.test(canvasSrc),
        failHint(SMOKE_ISSUES.canvasKeyboard, "star-graph-canvas.tsx missing accessible name"),
      ).toBe(true);
    },
  );

  // LRM-1105 #2010 + LRM-1190: Home/End live in canvas-keyboard-nav; canvas wires resolveCanvasKeyEvent.
  // Do NOT require research-canvas.tsx literal `e.key === "Home"|"End"` (false-positives from
  // isLogicEndNode / onMoveEnd).
  it(
    `${SMOKE_ISSUES.canvasKeyboard}: Home/End jump via canvas-keyboard-nav + resolveCanvasKeyEvent`,
    () => {
      const canvasSrc = readResearchSource(D5_CANVAS_SOURCE);
      const navSrc = readResearchSource("lib/canvas-keyboard-nav.ts");
      expect(
        canvasSrc.includes("resolveCanvasKeyEvent"),
        failHint(
          SMOKE_ISSUES.canvasKeyboard,
          "star-graph-canvas.tsx missing resolveCanvasKeyEvent wiring",
        ),
      ).toBe(true);
      for (const key of ["Home", "End"]) {
        expect(
          navSrc.includes(`"${key}"`) || navSrc.includes(`'${key}'`),
          failHint(SMOKE_ISSUES.canvasKeyboard, `canvas-keyboard-nav.ts missing ${key}`),
        ).toBe(true);
      }
      const { nodes, edges } = keyboardForkFixture();
      const ctx: CanvasKeyboardContext = { nodes, edges, focusId: "merge", overlay: null };
      expect(
        resolveCanvasKeyEvent({ key: "Home", shiftKey: false }, ctx),
        failHint(SMOKE_ISSUES.canvasKeyboard, "Home should jump to first main-chain node"),
      ).toEqual({ type: "moveFocus", nodeId: "goal" });
      expect(
        resolveCanvasKeyEvent({ key: "End", shiftKey: false }, { ...ctx, focusId: "goal" }),
        failHint(SMOKE_ISSUES.canvasKeyboard, "End should jump to last main-chain node"),
      ).toEqual({ type: "moveFocus", nodeId: "merge" });
    },
  );
});

/** Minimal fork for 1105 helper smoke (goal→fork→a|b→merge). */
function keyboardForkFixture(): {
  nodes: ResearchGraphNode[];
  edges: import("@multica/core/types").ResearchGraphEdge[];
} {
  const base = {
    session_id: "s1",
    summary: "",
    status: "active" as const,
    actor_agent_id: null,
    payload: {},
    created_at: "2026-07-31T00:00:00Z",
    updated_at: "2026-07-31T00:00:00Z",
  };
  const nodes: ResearchGraphNode[] = [
    { ...base, id: "goal", title: "Goal", node_type: "goal" },
    { ...base, id: "fork", title: "Fork", node_type: "stage_gate" },
    {
      ...base,
      id: "a",
      title: "Lane A",
      node_type: "probe",
      payload: { logic_lane: "source" },
    },
    {
      ...base,
      id: "b",
      title: "Lane B",
      node_type: "finding",
      payload: { logic_lane: "deep_read" },
    },
    { ...base, id: "merge", title: "Merge", node_type: "finding" },
  ];
  const edges = [
    {
      id: "e1",
      session_id: "s1",
      from_node_id: "goal",
      to_node_id: "fork",
      edge_type: "leads_to" as const,
      created_at: "2026-07-31T00:00:00Z",
    },
    {
      id: "e2",
      session_id: "s1",
      from_node_id: "fork",
      to_node_id: "a",
      edge_type: "leads_to" as const,
      created_at: "2026-07-31T00:00:00Z",
    },
    {
      id: "e3",
      session_id: "s1",
      from_node_id: "fork",
      to_node_id: "b",
      edge_type: "leads_to" as const,
      created_at: "2026-07-31T00:00:00Z",
    },
    {
      id: "e4",
      session_id: "s1",
      from_node_id: "a",
      to_node_id: "merge",
      edge_type: "leads_to" as const,
      created_at: "2026-07-31T00:00:00Z",
    },
    {
      id: "e5",
      session_id: "s1",
      from_node_id: "b",
      to_node_id: "merge",
      edge_type: "leads_to" as const,
      created_at: "2026-07-31T00:00:00Z",
    },
  ];
  return { nodes, edges };
}

function thirtyNodeFixture(): {
  nodes: ResearchGraphNode[];
  edges: import("@multica/core/types").ResearchGraphEdge[];
} {
  // Root + 29 same-type siblings → same layer:lane bucket, which today stacks
  // with a 10px offset and overlaps card AABBs (planar AC forbids this).
  const nodes: ResearchGraphNode[] = [
    {
      id: "root",
      session_id: "s1",
      title: "Goal",
      summary: "root",
      status: "active",
      node_type: "goal",
      actor_agent_id: null,
      payload: {},
      created_at: "2026-07-31T00:00:00Z",
      updated_at: "2026-07-31T00:00:00Z",
    },
    ...Array.from({ length: 29 }, (_, i) => ({
      id: `n${i}`,
      session_id: "s1",
      title: `Probe ${i}`,
      summary: `summary-${i}`,
      status: i % 5 === 0 ? "failed" : "active",
      node_type: "probe" as const,
      actor_agent_id: null,
      payload: {},
      created_at: "2026-07-31T00:00:00Z",
      updated_at: "2026-07-31T00:00:00Z",
    })),
  ];
  const edges = nodes.slice(1).map((n, i) => ({
    id: `e${i}`,
    session_id: "s1",
    from_node_id: "root",
    to_node_id: n.id,
    edge_type: "leads_to" as const,
    created_at: "2026-07-31T00:00:00Z",
  }));
  return { nodes, edges };
}

describe(`Smoke · canvas planar / actions (${SMOKE_ISSUES.canvasPlanar})`, () => {
  it(`${SMOKE_ISSUES.canvasPlanar}: planar keyboard + action contracts freeze`, () => {
    expect(PLANAR_KEYBOARD_CONTRACT.ArrowUp).toBe("topology-prev");
    expect(PLANAR_KEYBOARD_CONTRACT.ArrowDown).toBe("topology-next");
    expect(PLANAR_KEYBOARD_CONTRACT.ArrowLeft).toBe("branch-prev");
    expect(PLANAR_KEYBOARD_CONTRACT.ArrowRight).toBe("branch-next");
    expect(PLANAR_KEYBOARD_CONTRACT.Enter).toBe("open-detail-drawer");
    expect(PLANAR_KEYBOARD_CONTRACT.Escape).toBe("dismiss-layer");
    expect(PLANAR_KEYBOARD_CONTRACT["Shift+F10"]).toBe("open-context-menu");
    expect(ACTION_VISIBILITY_CONTRACT.gatedByStatusOrPermission).toBe(true);
    expect(ACTION_VISIBILITY_CONTRACT.destructiveNeedsConfirmOrUndo).toBe(true);
    expect(BRANCH_VS_STATUS_COLOR_CONTRACT.branchTokens.length).toBeGreaterThan(0);
  });

  // LRM-1091 planar layout ships — hard gate now.
  it(
    `${SMOKE_ISSUES.canvasPlanar}: 30-node layout has no card AABB overlap / pierce`,
    () => {
      const { nodes, edges } = thirtyNodeFixture();
      const laid = layoutResearchGraph(nodes, edges, { includeEnd: false });
      const cards = laid.nodes.filter((n) => n.type === "research");
      expect(cards.length).toBe(30);
      const rects: SmokeRect[] = cards.map((n) => ({
        id: n.id,
        x: n.position.x,
        y: n.position.y,
        w: Number(n.style?.width ?? RESEARCH_NODE_WIDTH),
        h: RESEARCH_NODE_HEIGHT,
      }));
      const overlaps = findOverlappingPairs(rects);
      expect(
        overlaps,
        failHint(
          SMOKE_ISSUES.canvasPlanar,
          `overlapping pairs: ${overlaps.map(([a, b]) => `${a}/${b}`).join(", ")}`,
        ),
      ).toEqual([]);
    },
  );

  // #1956: leads_to uses --brand (not status success/warning/destructive) — hard gate.
  it(`${SMOKE_ISSUES.canvasPlanar}: leads_to edge stroke must not reuse status accent tokens`, () => {
    const branchStroke = visualForEdgeType("leads_to").stroke;
    const statusAccents = [
      visualForNodeType("finding").accentBarClass,
      visualForNodeType("conflict").accentBarClass,
      visualForNodeType("dead_end").accentBarClass,
    ];
    for (const accent of statusAccents) {
      expect(
        branchStroke.includes("success") ||
          branchStroke.includes("warning") ||
          branchStroke.includes("destructive") ||
          accent.includes(branchStroke.replace(/var\(|\)/g, "")),
        failHint(
          SMOKE_ISSUES.canvasPlanar,
          `branch stroke ${branchStroke} collides with status accent ${accent}`,
        ),
      ).toBe(false);
    }
  });

  // LRM-1091 planar keyboard wiring shipped — hard gate now.
  // LRM-1105 slice3: key literals live in canvas-keyboard-nav; canvas only wires resolveCanvasKeyEvent.
  it(
    `${SMOKE_ISSUES.canvasPlanar}: D5 canvas wires keyboard map via resolveCanvasKeyEvent (shared nav contract)`,
    () => {
      const canvasSrc = readResearchSource(D5_CANVAS_SOURCE);
      const navSrc = readResearchSource("lib/canvas-keyboard-nav.ts");
      expect(
        canvasSrc.includes("resolveCanvasKeyEvent"),
        failHint(
          SMOKE_ISSUES.canvasPlanar,
          "star-graph-canvas.tsx missing resolveCanvasKeyEvent wiring",
        ),
      ).toBe(true);
      for (const key of ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Enter", "Escape"]) {
        expect(
          navSrc.includes(key),
          failHint(SMOKE_ISSUES.canvasPlanar, `canvas-keyboard-nav.ts missing ${key}`),
        ).toBe(true);
      }
      expect(
        /Shift\+F10|shiftKey.*F10|F10/.test(navSrc),
        failHint(
          SMOKE_ISSUES.canvasPlanar,
          "canvas-keyboard-nav.ts missing Shift+F10 context menu",
        ),
      ).toBe(true);
    },
  );

  // #1956 / LRM-981: retry gated by failed|error|dead_end|refuted — hard gate.
  it(`${SMOKE_ISSUES.canvasPlanar}: ring retry gated by node status`, () => {
    const active = ringActionsForNode({
      id: "a",
      session_id: "s1",
      title: "Active probe",
      summary: "",
      status: "active",
      node_type: "probe",
      actor_agent_id: null,
      payload: {},
      created_at: "2026-07-31T00:00:00Z",
      updated_at: "2026-07-31T00:00:00Z",
    });
    const failed = ringActionsForNode({
      id: "b",
      session_id: "s1",
      title: "Failed",
      summary: "",
      status: "failed",
      node_type: "probe",
      actor_agent_id: null,
      payload: {},
      created_at: "2026-07-31T00:00:00Z",
      updated_at: "2026-07-31T00:00:00Z",
    });
    const activeRetry = active.find((a) => a.id === "retry");
    expect(
      !activeRetry || activeRetry.disabled === true,
      failHint(SMOKE_ISSUES.canvasPlanar, "active probe should not expose live retry"),
    ).toBe(true);
    expect(
      failed.find((a) => a.id === "retry")?.disabled,
      failHint(SMOKE_ISSUES.canvasPlanar, "failed probe should enable retry"),
    ).toBeFalsy();
    expect(ACTION_VISIBILITY_CONTRACT.destructiveActionIds.length).toBeGreaterThan(0);
  });

  // Ring recover actions now advertise confirm (reassign); keep as a hard gate.
  it(
    `${SMOKE_ISSUES.canvasPlanar}: destructive ring/canvas path has confirm/undo hook`,
    () => {
      const ringSrc = readResearchSource("lib/node-action-ring.ts");
      const canvasSrc = readResearchSource(D5_CANVAS_SOURCE);
      const hasConfirmOrUndo =
        /confirm|undo|AlertDialog|destructiveNeedsConfirm/i.test(ringSrc) ||
        /confirm|undo|AlertDialog/i.test(canvasSrc);
      expect(
        hasConfirmOrUndo,
        failHint(
          SMOKE_ISSUES.canvasPlanar,
          "destructive canvas actions missing confirm or undo hook",
        ),
      ).toBe(true);
    },
  );
});
