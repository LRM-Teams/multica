// @vitest-environment jsdom

/**
 * LRM-1197 — [巡检][F] no-login static a11y for empty + permission/read-only states.
 * Source scan + render asserts; does not touch research-f-state-a11y* or raw-hex guards.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { cardMenuItemsForNode } from "../lib/card-menu-actions";
import { ResearchCanvasEmptyState } from "./research-canvas-empty-state";
import { ResearchCardMenu } from "./research-card-menu";
import { ResearchEmptyState } from "./research-empty-state";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        empty_title: "Start a research session",
        empty_desc: "Describe a goal to explore.",
        empty_examples_label: "Try an example",
        empty_examples: {
          q1: "Example one",
          q2: "Example two",
          q3: "Example three",
          q4: "Example four",
        },
        empty_cta: "Start research",
        home_empty: {
          path: "Research path",
          ask: "Ask",
          assign: "Assign",
          verify: "Verify",
          deliver: "Deliver",
        },
        node: { goal: "Goal", probe: "Probe" },
        logic: { lane: { source: "Source" } },
        session_page: {
          canvas_empty_title: "Canvas is empty",
          canvas_empty_body: "Create a goal node to begin.",
          canvas_empty_home: "Back to list",
          canvas_empty_create: "Create goal",
          canvas_empty_creating: "Creating…",
          canvas_empty_create_goal: "New research goal",
        },
        card_menu: {
          view_evidence: "View evidence",
          view_io: "View I/O",
          fork_from: "Fork from here",
          retry_failed: "Retry",
          reassign: "Reassign",
          cancel_run: "Cancel run",
          confirm: "Confirm {{action}}?",
        },
      }),
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    research: () => "/ws/research",
    researchDetail: (id: string) => `/ws/research/${id}`,
  }),
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    createResearchSession: vi.fn(),
  },
}));

const EMPTY_FILES = [
  "research-empty-state.tsx",
  "research-canvas-empty-state.tsx",
  "research-card-menu.tsx",
] as const;

function makeNode(
  partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "title">,
): ResearchGraphNode {
  return {
    session_id: "s1",
    node_type: "probe",
    summary: "",
    status: "done",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-03T00:00:00Z",
    updated_at: "2026-08-03T00:00:00Z",
    ...partial,
  };
}

describe("research empty/permission a11y static contract (LRM-1197)", () => {
  it("bans sm structural visibility flips on empty/permission sources", () => {
    for (const file of EMPTY_FILES) {
      const src = readSrc(file);
      expect(src, file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: list empty exposes aria-label; decorative mini-canvas is aria-hidden", () => {
    const src = readSrc("research-empty-state.tsx");
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.empty_title\)\}/);
    expect(src).toMatch(/aria-hidden/);
    expect(src).toMatch(/data-testid=["']research-empty-state["']/);
  });

  it("source: canvas empty keeps decorative aria-hidden + focusable CTAs", () => {
    const src = readSrc("research-canvas-empty-state.tsx");
    expect(src).toMatch(/aria-hidden/);
    expect(src).toMatch(/data-testid=["']research-canvas-empty-home["']/);
    expect(src).toMatch(/data-testid=["']research-canvas-empty-create["']/);
    expect(src).toMatch(/canvas_empty_title/);
  });

  it("source: filter no-results announces politely via <output aria-live=polite>", () => {
    const src = readSrc("research-list-page.tsx");
    expect(src).toMatch(
      /data-testid=["']research-filter-no-results["'][\s\S]{0,120}aria-live=["']polite["']|<output[\s\S]{0,80}aria-live=["']polite["'][\s\S]{0,120}research-filter-no-results/,
    );
  });

  it("source: card menu surfaces disabledReason via Tooltip + visible text", () => {
    const src = readSrc("research-card-menu.tsx");
    expect(src).toMatch(/TooltipContent side="top">\{item\.disabledReason\}/);
    expect(src).toMatch(/item\.disabledReason/);
    expect(src).toMatch(/disabled=\{!item\.enabled\}/);
  });

  it("permission: canWrite=false disables retry with explicit read-only reason", () => {
    const failed = makeNode({
      id: "f1",
      title: "Failed probe",
      status: "failed",
      node_type: "dead_end",
    });
    const items = cardMenuItemsForNode(failed, { canWrite: false });
    const retry = items.find((i) => i.id === "retry_failed");
    expect(retry?.enabled).toBe(false);
    expect(retry?.disabledReason).toMatch(
      /read-only|no write permission|无写入权限/i,
    );

    const cancel = items.find((i) => i.id === "cancel_run");
    expect(cancel?.enabled).toBe(false);
    expect(cancel?.disabledReason).toBeTruthy();
  });

  it("render: list empty has labeled region, hidden decoration, focusable CTAs", () => {
    render(
      <ResearchEmptyState onSelectExample={() => {}} onStart={() => {}} />,
    );
    const root = screen.getByTestId("research-empty-state");
    expect(root.tagName.toLowerCase()).toBe("section");
    expect(root.getAttribute("aria-label")).toBe("Start a research session");
    expect(root.querySelector("[aria-hidden]")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Start research" }),
    ).toBeEnabled();
    expect(screen.getByRole("button", { name: "Example one" })).toBeEnabled();
  });

  it("render: canvas empty shows title + Home/Create buttons", () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(
      <QueryClientProvider client={qc}>
        <ResearchCanvasEmptyState />
      </QueryClientProvider>,
    );
    expect(screen.getByText("Canvas is empty")).toBeTruthy();
    expect(screen.getByTestId("research-canvas-empty-home")).toBeEnabled();
    expect(screen.getByTestId("research-canvas-empty-create")).toBeEnabled();
    expect(
      screen.getByTestId("research-session-canvas-empty").querySelector("[aria-hidden]"),
    ).toBeTruthy();
  });

  it("render: disabled card-menu items expose disabledReason (not silent)", () => {
    const node = makeNode({
      id: "n1",
      title: "Probe",
      status: "failed",
      node_type: "dead_end",
    });
    render(
      <ResearchCardMenu node={node} onClose={() => {}} />,
    );
    const fork = screen.getByRole("menuitem", { name: /Fork from here/i });
    expect(fork).toBeDisabled();
    // Native hover title was migrated to the shared Tooltip (content renders on hover);
    // the accessible/visible reason is still exposed as inline text below the label.
    expect(fork.getAttribute("title")).toBeNull();
    expect(fork.textContent).toMatch(/not available|不可创建探索分支/i);
  });
});
