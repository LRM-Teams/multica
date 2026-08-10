// @vitest-environment jsdom

/**
 * LRM-1202 — [巡检][F] no-login static a11y for session chrome / goal / members.
 * Source scan + render asserts; companion to LRM-1159 (true-device chrome gate).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { ResearchSession } from "@multica/core/types";
import { ResearchFleetAvatarStack } from "./research-fleet-avatar-stack";
import { ResearchSessionChrome } from "./research-session-chrome";
import { ResearchSessionGoalCard } from "./research-session-goal-card";
import { ResearchSessionMetaMenu } from "./research-session-meta-menu";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1 / sm:gap-*. */
const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

const mobileState = { isMobile: false };

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const raw = fn({
        status: {
          running: "Running",
          awaiting_user_confirm: "Awaiting confirm",
          completed: "Completed",
          paused: "Paused",
        },
        stage: {
          s1_plan: "S1 · Plan",
          s2_sources: "S2 · Explore",
          s3_validation: "S3 · Validate",
          s4_delivery: "S4 · Deliver",
        },
        stage_short: {
          s1_plan: "S1",
          s2_sources: "S2",
          s3_validation: "S3",
          s4_delivery: "S4",
        },
        timeline: {
          label: "Research stages",
          done: "Done",
          current: "Current",
          upcoming: "Upcoming",
          done_feedback: "Stage completed",
        },
        panel: {
          confirm_continue: "Confirm & continue",
          gate_approve: "Approve",
          gate_reject: "Reject",
          gate_reject_title: "Reject confirmation",
          gate_reject_hint: "Optional feedback.",
          gate_reject_placeholder: "What should revise?",
          gate_reject_submit: "Send rejection",
          gate_reject_submitting: "Sending…",
          handoff_title: "Handoff delivery",
          view_delivery: "View delivery",
          handoff_project: "Create development project",
          handoff_channel: "Create development channel",
          handoff: "Handoff",
          session_tools: "Session tools",
          fleet: "Fleet",
          sources: "Sources",
          session_tools_hint: "On demand",
          fleet_mode: {
            empty: "Idle",
            loading: "Assembling",
            running: "Running",
            done: "Done",
          },
        },
        round: {
          subtitle: "Product rounds",
          budget_chip: "{{used}}/{{budget}}",
          budget_capped: "capped",
        },
        create_params: {
          session_menu: "Create parameters",
          session_hint: "Read-only",
          depth_label: "Depth",
          language_label: "Language",
          weights_label: "Weights",
          chip_depth: "Depth · {{label}}",
          depth_tiers: {
            shallow: { label: "Shallow", hint: "2" },
            standard: { label: "Standard", hint: "5" },
            deep: { label: "Deep", hint: "10" },
          },
          language_options: { zh: "中文", en: "English" },
          weight_rows: {
            primary: { label: "Primary", hint: "Official" },
            secondary: { label: "Secondary", hint: "Reviews" },
            community: { label: "Community", hint: "Forums" },
          },
        },
        goal_card: {
          label: "GOAL",
          final_title: "Final goal",
          card_title: "View final goal",
          icon_title: "Final goal",
          empty_summary: "Not converged",
          loading_summary: "Updating…",
          error_summary: "Goal failed",
          pending_summary: "Pending…",
          empty_body: "Goal empty.",
          loading_body: "Updating…",
          error_body: "Retry.",
          optimized_note: "Optimized",
          previous_label: "Previous",
          substantive_label: "Pending change",
          close: "Close",
          retry: "Retry",
          confirm_substantive: "Confirm",
          collapse_icon: "Collapse",
          expand_card: "Expand",
        },
        overlay: { fleet_collapse: "Collapse fleet", fleet_expand: "Expand fleet" },
      });
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid={`avatar-${actorId}`}>avatar</span>
  ),
}));

vi.mock("@multica/ui/components/ui/popover", async () => {
  const React = await import("react");
  const Ctx = React.createContext<{
    open: boolean;
    onOpenChange?: (open: boolean) => void;
  }>({ open: false });
  return {
    Popover: ({
      open,
      onOpenChange,
      children,
    }: {
      open?: boolean;
      onOpenChange?: (open: boolean) => void;
      children?: React.ReactNode;
    }) => (
      <Ctx.Provider value={{ open: open ?? false, onOpenChange }}>{children}</Ctx.Provider>
    ),
    PopoverTrigger: ({
      render: renderProp,
      children,
    }: {
      render?: React.ReactElement;
      children?: React.ReactNode;
    }) => {
      const { open, onOpenChange } = React.useContext(Ctx);
      return React.cloneElement(
        renderProp ?? <button type="button" />,
        { onClick: () => onOpenChange?.(!open) } as Record<string, unknown>,
        children,
      );
    },
    PopoverContent: ({ children }: { children?: React.ReactNode }) => {
      const { open } = React.useContext(Ctx);
      return open ? <div>{children}</div> : null;
    },
    PopoverHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    PopoverTitle: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    PopoverDescription: ({ children }: { children?: React.ReactNode }) => (
      <div>{children}</div>
    ),
  };
});

vi.mock("@multica/ui/components/ui/dropdown-menu", async () => {
  const React = await import("react");
  return {
    DropdownMenu: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuTrigger: ({
      render: renderProp,
      children,
    }: {
      render?: React.ReactElement;
      children?: React.ReactNode;
    }) => React.cloneElement(renderProp ?? <button type="button" />, {}, children),
    DropdownMenuContent: ({ children }: { children?: React.ReactNode }) => (
      <div data-testid="menu">{children}</div>
    ),
    DropdownMenuItem: ({
      children,
      onClick,
    }: {
      children?: React.ReactNode;
      onClick?: () => void;
    }) => (
      <button type="button" onClick={onClick}>
        {children}
      </button>
    ),
    DropdownMenuSeparator: () => <hr />,
  };
});

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: () => null,
  SheetContent: () => null,
  SheetHeader: () => null,
  SheetTitle: () => null,
  SheetDescription: () => null,
}));

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children?: React.ReactNode }) => (
    <div data-testid="goal-dialog">{children}</div>
  ),
  DialogHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  DialogDescription: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  DialogFooter: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
}));

const SOURCE_FILES = [
  "research-session-chrome.tsx",
  "research-session-goal-card.tsx",
  "research-session-meta-menu.tsx",
  "research-fleet-avatar-stack.tsx",
] as const;

function makeSession(overrides: Partial<ResearchSession> = {}): ResearchSession {
  return {
    id: "s1",
    workspace_id: "w1",
    fleet_id: "f1",
    created_by: "u1",
    title: "知春路沿线房产市场深度调研",
    goal: "分析知春路沿线 3 公里二手房挂牌与成交",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T00:00:00Z",
    ...overrides,
  };
}

describe("research session chrome a11y static contract (LRM-1202)", () => {
  beforeEach(() => {
    mobileState.isMobile = false;
    window.localStorage.clear();
  });

  it("bans sm structural visibility flips on chrome/goal/meta/fleet sources", () => {
    for (const file of SOURCE_FILES) {
      const src = readSrc(file);
      expect(src, file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: chrome uses header landmark + decorative aria-hidden", () => {
    const src = readSrc("research-session-chrome.tsx");
    expect(src).toMatch(/<header[\s\S]*data-testid=["']research-session-chrome["']/);
    expect(src).toMatch(/<h1[\s>]/);
    expect(src).toMatch(/aria-hidden/);
  });

  it("source: session tools trigger has aria-label; fleet toggle has expand/collapse label", () => {
    expect(readSrc("research-session-meta-menu.tsx")).toMatch(/aria-label=\{t\(\(\$\) => \$\.panel\.session_tools\)\}/);
    const fleet = readSrc("research-fleet-avatar-stack.tsx");
    expect(fleet).toMatch(/aria-label=\{open \? t\(\(\$\) => \$\.overlay\.fleet_collapse\)/);
    expect(fleet).toMatch(/fleet_expand/);
  });

  it("render: chrome exposes header, h1 title, visible status, and decorative aria-hidden", () => {
    render(
      <ResearchSessionChrome
        session={makeSession()}
        canConfirm={false}
        canHandoff={false}
        createProject={false}
        createChannel={false}
        onCreateProjectChange={() => {}}
        onCreateChannelChange={() => {}}
        onConfirm={() => {}}
        onHandoff={() => {}}
        onSelectStage={vi.fn()}
      />,
    );

    const chrome = screen.getByTestId("research-session-chrome");
    expect(chrome.tagName).toBe("HEADER");

    const title = within(chrome).getByRole("heading", { level: 1 });
    expect(title).toHaveTextContent("知春路沿线房产市场深度调研");

    const status = within(chrome).getByTestId("research-session-status");
    expect(status).toHaveTextContent("Running");

    const hidden = chrome.querySelectorAll("[aria-hidden]");
    expect(hidden.length).toBeGreaterThan(0);
  });

  it("render: interactive stage step is a focusable button", () => {
    render(
      <ResearchSessionChrome
        session={makeSession()}
        canConfirm={false}
        canHandoff={false}
        createProject={false}
        createChannel={false}
        onCreateProjectChange={() => {}}
        onCreateChannelChange={() => {}}
        onConfirm={() => {}}
        onHandoff={() => {}}
        onSelectStage={vi.fn()}
      />,
    );
    const stage = screen.getByRole("button", { name: /S2 · Explore/ });
    expect(stage.tagName).toBe("BUTTON");
    expect(stage).not.toHaveAttribute("disabled");
    stage.focus();
    expect(document.activeElement).toBe(stage);
  });

  it("render: Goal Card trigger has an accessible name (title)", () => {
    render(
      <ResearchSessionGoalCard
        sessionId="s1"
        goal="分析知春路沿线 3 公里二手房挂牌与成交"
      />,
    );
    const card = screen.getByTestId("research-session-goal-card");
    expect(card).toHaveAttribute("title", "View final goal");
    expect(
      screen.getByRole("button", { name: /View final goal|GOAL|分析知春路/ }),
    ).toBe(card);
  });

  it("render: Session tools trigger exposes aria-label", () => {
    render(<ResearchSessionMetaMenu members={[]} sources={[]} />);
    const tools = screen.getByTestId("research-session-tools");
    expect(tools).toHaveAttribute("aria-label", "Session tools");
    expect(screen.getByRole("button", { name: "Session tools" })).toBe(tools);
  });

  it("render: fleet stack with members exposes labeled expand control", () => {
    render(
      <ResearchFleetAvatarStack
        members={[
          {
            id: "m1",
            agent_id: "agent-1",
            name: "Scout",
            display_name: "Scout",
            role: "scout",
            status: "active",
            is_lead: false,
          },
        ]}
        sessionStatus="running"
      />,
    );
    const toggle = screen.getByTestId("research-fleet-avatar-stack-toggle");
    expect(toggle).toHaveAttribute("aria-label", "Expand fleet");
    expect(screen.getByRole("button", { name: "Expand fleet" })).toBe(toggle);
  });

  it("render: fleet empty mode is text-only (no focusable trap)", () => {
    // empty only when not in-flight; running+0 members → loading (LRM-980).
    render(<ResearchFleetAvatarStack members={[]} sessionStatus="completed" />);
    const stack = screen.getByTestId("research-fleet-avatar-stack");
    expect(stack).toHaveAttribute("data-fleet-mode", "empty");
    expect(within(stack).queryByRole("button")).toBeNull();
    expect(stack).toHaveTextContent("Idle");
  });

  it("render: fleet loading (in-flight, no members) is busy and non-interactive", () => {
    render(<ResearchFleetAvatarStack members={[]} sessionStatus="running" />);
    const stack = screen.getByTestId("research-fleet-avatar-stack");
    expect(stack).toHaveAttribute("data-fleet-mode", "loading");
    expect(stack).toHaveAttribute("aria-busy");
    expect(within(stack).queryByRole("button")).toBeNull();
  });
});
