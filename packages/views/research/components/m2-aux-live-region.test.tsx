// @vitest-environment jsdom

/**
 * LRM-1201 — [巡检][D] M2 aux cards must keep a persistent live region.
 *
 * Root cause guarded here: `aria-live` / `aria-busy` used to sit on the
 * `if (mode === "loading")` early-return subtree, so the live region node was
 * unmounted at the exact frame content became ready. A live region that is
 * inserted together with its text is not announced, and one that is removed on
 * completion can never announce the result.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type {
  ExplorationDimension,
  HumanBoundaryModel,
  SourceStrategyModel,
} from "../lib/m2-visibility";
import { ExplorationRail } from "./exploration-rail";
import { HumanBoundaryCard } from "./human-boundary-card";
import { SourceStrategyStrip } from "./source-strategy-strip";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(file: string) {
  return fs.readFileSync(path.join(here, file), "utf8");
}

const M2 = {
  rail_title: "Exploration trail",
  rail_hint: "By dimension family",
  rail_empty_title: "No trail yet",
  rail_empty_body: "Kick off to populate.",
  rail_empty_expect_verified: "Verified directions",
  rail_empty_expect_gap: "Questions needing evidence",
  rail_empty_expect_reuse: "Reusable findings",
  rail_loading: "Building the question tree…",
  rail_loading_hint: "Results appear when ready",
  rail_ready_live: "Exploration trail ready",
  rail_error: "Trail failed to load",
  rail_error_title: "Could not organize the exploration trail",
  rail_error_body: "Please try again later",
  rail_question_count: "nodes",
  rail_summary_pending: "Summary pending",
  rail_summary_verified: "{{count}} directions verified",
  rail_summary_adopted: "{{count}} findings adopted",
  rail_summary_dead: "{{count}} without a usable conclusion",
  rail_summary_joiner: " · ",
  rail_completed_banner: "This research is complete",
  rail_completed_directions: "{{count}} directions",
  rail_completed_findings: "{{count}} findings",
  rail_result_prefix: "Result: ",
  rail_result_open: "Collecting evidence",
  rail_result_covered_fallback: "A usable conclusion is ready",
  rail_result_gap: "Evidence is insufficient",
  rail_result_dead: "No usable conclusion yet",
  rail_result_dead_reason: "Reason: {{reason}}",
  rail_next_expand_covered: "{{count}} questions · expand",
  rail_next_expand_gap: "{{count}} questions · expand gaps",
  rail_next_expand_dead: "{{count}} questions · collapse",
  rail_collapse: "Collapse",
  required: "Required",
  status: { open: "Open", covered: "Covered", gap: "Gap", dead: "Dead" },
  strategy_title: "Which sources informed this research",
  strategy_label: "Source strategy",
  strategy_hint: "See what the conclusions drew on.",
  strategy_empty_title: "No research evidence to show yet",
  strategy_empty_body: "Fleet will write sources.",
  strategy_loading: "Gathering research evidence",
  strategy_partial: "Some evidence is in; research is still filling gaps",
  strategy_ready_status: "Research evidence is ready",
  strategy_ready_live: "Research evidence is ready",
  strategy_error: "Could not load research evidence",
  strategy_expect_1: "Source layers",
  strategy_expect_2: "Adoption reasons",
  strategy_expect_3: "Sample links",
  strategy_sample_count: "samples",
  strategy_summary_pending: "Summary pending",
  layer_general: "General reference",
  layer_domain: "Domain evidence",
  why_label: "Why here:",
  boundary_primary_title: "What humans and Agents each own",
  boundary_title: "Human / AI boundary",
  boundary_hint: "Clarify Agent assist vs human confirmation.",
  boundary_chip: "Confirm before delivery",
  boundary_empty_title: "No collaboration split to show yet",
  boundary_empty_body: "Delivery stage fills this in.",
  boundary_loading: "Clarifying roles and constraints",
  boundary_partial: "Some evidence is in; research is still filling gaps",
  boundary_ready_status: "Collaboration split is clear",
  boundary_ready_live: "Collaboration split is clear",
  boundary_error: "Could not load the collaboration split",
  boundary_expect_1: "Agent limits",
  boundary_expect_2: "Human confirmation",
  boundary_expect_3: "How they collaborate",
  boundary_summary_pending: "Pending",
  boundary_matrix_label: "How humans and Agents collaborate",
  ai_ceiling: "Agent limits",
  must_human: "Needs human confirmation",
  col_human: "Human",
  col_ai: "AI",
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const out = fn({ m2: M2, session_page: { retry: "Retry" } });
      if (typeof out === "string" && vars) {
        return out.replace(/\{\{(\w+)\}\}/g, (_, k: string) =>
          String(vars[k] ?? ""),
        );
      }
      return out;
    },
  }),
}));

const EMPTY_BOUNDARY: HumanBoundaryModel = {
  aiCeiling: "",
  mustHuman: "",
  matrix: [],
  empty: true,
};

const READY_BOUNDARY: HumanBoundaryModel = {
  aiCeiling: "Can draft",
  mustHuman: "Must sign off",
  matrix: [{ human: "Decide", ai: "Summarise" }],
  empty: false,
};

const EMPTY_STRATEGY: SourceStrategyModel = {
  chips: [],
  whyLine: "",
  empty: true,
};

const READY_STRATEGY: SourceStrategyModel = {
  chips: [
    { id: "c1", label: "Vendor docs", layer: "domain", why: "Primary", samples: [] },
  ],
  whyLine: "Domain first",
  empty: false,
};

const EMPTY_DIMENSIONS: ExplorationDimension[] = [];

const READY_DIMENSIONS: ExplorationDimension[] = [
  {
    family: "market",
    title: "Market",
    status: "open",
    questions: [{ id: "q1", title: "Who buys?", nodeType: "question" }],
  },
];

type Case = {
  name: string;
  rootTestId: string;
  liveTestId: string;
  loadingText: string;
  readyText: string;
  emptyText: string;
  renderLoading: () => ReturnType<typeof render>;
  rerenderReady: (r: ReturnType<typeof render>) => void;
  rerenderEmpty: (r: ReturnType<typeof render>) => void;
};

const CASES: Case[] = [
  {
    name: "HumanBoundaryCard",
    rootTestId: "human-boundary-card",
    liveTestId: "human-boundary-live",
    loadingText: M2.boundary_loading,
    readyText: M2.boundary_ready_live,
    emptyText: M2.boundary_empty_title,
    renderLoading: () =>
      render(<HumanBoundaryCard model={EMPTY_BOUNDARY} sessionStatus="running" />),
    rerenderReady: (r) =>
      r.rerender(
        <HumanBoundaryCard model={READY_BOUNDARY} sessionStatus="done" />,
      ),
    rerenderEmpty: (r) =>
      r.rerender(<HumanBoundaryCard model={EMPTY_BOUNDARY} sessionStatus="done" />),
  },
  {
    name: "SourceStrategyStrip",
    rootTestId: "source-strategy-strip",
    liveTestId: "source-strategy-live",
    loadingText: M2.strategy_loading,
    readyText: M2.strategy_ready_live,
    emptyText: M2.strategy_empty_title,
    renderLoading: () =>
      render(<SourceStrategyStrip model={EMPTY_STRATEGY} sessionStatus="running" />),
    rerenderReady: (r) =>
      r.rerender(
        <SourceStrategyStrip model={READY_STRATEGY} sessionStatus="done" />,
      ),
    rerenderEmpty: (r) =>
      r.rerender(<SourceStrategyStrip model={EMPTY_STRATEGY} sessionStatus="done" />),
  },
  {
    name: "ExplorationRail",
    rootTestId: "exploration-rail",
    liveTestId: "exploration-rail-live",
    loadingText: M2.rail_loading,
    readyText: M2.rail_ready_live,
    emptyText: M2.rail_empty_title,
    renderLoading: () =>
      render(<ExplorationRail dimensions={EMPTY_DIMENSIONS} sessionStatus="running" />),
    rerenderReady: (r) =>
      r.rerender(
        <ExplorationRail dimensions={READY_DIMENSIONS} sessionStatus="running" />,
      ),
    rerenderEmpty: (r) =>
      r.rerender(<ExplorationRail dimensions={EMPTY_DIMENSIONS} sessionStatus="done" />),
  },
];

describe("M2 aux cards keep a persistent live region (LRM-1201)", () => {
  for (const c of CASES) {
    it(`${c.name}: root and live region survive loading → ready`, () => {
      const r = c.renderLoading();

      const rootBefore = screen.getByTestId(c.rootTestId);
      const liveBefore = screen.getByTestId(c.liveTestId);
      expect(rootBefore.getAttribute("aria-busy")).toBe("true");
      expect(liveBefore.getAttribute("aria-live")).toBe("polite");
      // Announcement-only: must not add visible pixels to any state (AC5).
      expect(liveBefore.className).toContain("sr-only");
      expect(liveBefore.textContent).toBe(c.loadingText);

      c.rerenderReady(r);

      const rootAfter = screen.getByTestId(c.rootTestId);
      const liveAfter = screen.getByTestId(c.liveTestId);
      // Same nodes — a replaced live region cannot announce completion.
      expect(rootAfter).toBe(rootBefore);
      expect(liveAfter).toBe(liveBefore);
      expect(rootAfter.getAttribute("aria-busy")).toBe("false");
      expect(liveAfter.textContent).toBe(c.readyText);

      r.unmount();
    });

    it(`${c.name}: loading → empty also announces on the same node`, () => {
      const r = c.renderLoading();
      const liveBefore = screen.getByTestId(c.liveTestId);

      c.rerenderEmpty(r);

      const liveAfter = screen.getByTestId(c.liveTestId);
      expect(liveAfter).toBe(liveBefore);
      expect(liveAfter.textContent).toBe(c.emptyText);
      expect(screen.getByTestId(c.rootTestId).getAttribute("aria-busy")).toBe(
        "false",
      );

      r.unmount();
    });

    it(`${c.name}: loading → partial → ready keeps the same live node (LRM-1282)`, () => {
      if (c.rootTestId === "exploration-rail") return;

      const r = c.renderLoading();
      const liveBefore = screen.getByTestId(c.liveTestId);
      expect(liveBefore.textContent).toBe(c.loadingText);

      if (c.rootTestId === "human-boundary-card") {
        r.rerender(
          <HumanBoundaryCard model={READY_BOUNDARY} sessionStatus="running" />,
        );
        expect(screen.getByTestId(c.liveTestId)).toBe(liveBefore);
        expect(liveBefore.textContent).toBe(M2.boundary_partial);
        expect(screen.getByTestId(c.rootTestId).getAttribute("aria-busy")).toBe(
          "false",
        );
        r.rerender(
          <HumanBoundaryCard model={READY_BOUNDARY} sessionStatus="done" />,
        );
      } else {
        r.rerender(
          <SourceStrategyStrip model={READY_STRATEGY} sessionStatus="running" />,
        );
        expect(screen.getByTestId(c.liveTestId)).toBe(liveBefore);
        expect(liveBefore.textContent).toBe(M2.strategy_partial);
        expect(screen.getByTestId(c.rootTestId).getAttribute("aria-busy")).toBe(
          "false",
        );
        r.rerender(
          <SourceStrategyStrip model={READY_STRATEGY} sessionStatus="done" />,
        );
      }

      expect(screen.getByTestId(c.liveTestId)).toBe(liveBefore);
      expect(liveBefore.textContent).toBe(c.readyText);
      r.unmount();
    });

    it(`${c.name}: error keeps role=alert and does not double-announce politely`, () => {
      const r = render(<div />);
      r.unmount();

      if (c.rootTestId === "human-boundary-card") {
        render(
          <HumanBoundaryCard model={EMPTY_BOUNDARY} error="boom" />,
        );
      } else if (c.rootTestId === "source-strategy-strip") {
        render(<SourceStrategyStrip model={EMPTY_STRATEGY} error="boom" />);
      } else {
        render(<ExplorationRail dimensions={EMPTY_DIMENSIONS} error="boom" />);
      }

      // The visible error text stays inside an assertive alert.
      // ExplorationRail (LRM-1281/1287) never surfaces the raw error string.
      if (c.rootTestId === "exploration-rail") {
        expect(screen.getByRole("alert").textContent).toContain(
          "Could not organize the exploration trail",
        );
        expect(screen.getByRole("alert").textContent).not.toContain("boom");
      } else {
        expect(screen.getByRole("alert").textContent).toContain("boom");
      }
      // The polite region must stay silent so the message is not read twice.
      expect(screen.getByTestId(c.liveTestId).textContent).toBe("");
      screen.getByTestId(c.rootTestId);
    });
  }
});

const SOURCES = [
  "human-boundary-card.tsx",
  "source-strategy-strip.tsx",
  "exploration-rail.tsx",
] as const;

describe("M2 aux source guard: live region declared outside mode branches", () => {
  for (const file of SOURCES) {
    it(`${file} never puts aria-live / aria-busy inside a mode early return`, () => {
      const src = readSrc(file);
      const firstBranch = src.indexOf('if (mode === ');
      expect(firstBranch).toBeGreaterThan(0);

      const tail = src.slice(firstBranch);
      expect(tail).not.toMatch(/aria-live/);
      expect(tail).not.toMatch(/aria-busy/);

      const head = src.slice(0, firstBranch);
      expect(head).toMatch(/aria-live="polite"/);
      expect(head).toMatch(/aria-busy=/);
      expect(head).toMatch(/sr-only/);
    });
  }
});
