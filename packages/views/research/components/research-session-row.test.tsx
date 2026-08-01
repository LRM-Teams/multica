import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchSession } from "@multica/core/types";
import enResearch from "../../locales/en/research.json";

const avatarStackRef = vi.hoisted(() => ({
  agentIds: undefined as readonly string[] | undefined,
  className: undefined as string | undefined,
}));

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
  AgentAvatarStack: ({
    agentIds,
    className,
  }: {
    agentIds: readonly string[];
    className?: string;
  }) => {
    avatarStackRef.agentIds = agentIds;
    avatarStackRef.className = className;
    return agentIds.length > 0 ? (
      <span data-testid="avatar-stack" className={className} />
    ) : null;
  },
}));

vi.mock("./research-session-row-actions", () => ({
  ResearchSessionRowActions: () => <span data-testid="row-actions" />,
}));

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({
    open,
    children,
  }: {
    open: boolean;
    children: React.ReactNode;
  }) => (open ? <div data-testid="goal-dialog">{children}</div> : null),
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

import { ResearchSessionRow } from "./research-session-row";

function session(partial: Partial<ResearchSession> = {}): ResearchSession {
  return {
    id: "s1",
    workspace_id: "workspace-1",
    fleet_id: "fleet-1",
    created_by: "user-1",
    title: "Alpha market map",
    goal: "Map the alpha market across regions with pricing and share",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T03:00:00Z",
    fleet_preview: [
      { agent_id: "agent-1", display_name: "Ronaldo", is_lead: true },
      { agent_id: "agent-2", display_name: "Source" },
    ],
    ...partial,
  };
}

describe("ResearchSessionRow (LRM-790 narrow + dark tokens)", () => {
  it("running status paints a brand dot with motion-safe pulse", () => {
    const { container } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const dot = container.querySelector("span.rounded-full.size-2");
    expect(dot?.className).toContain("bg-brand");
    expect(dot?.className).toContain("motion-safe:animate-pulse");
    expect(screen.getByText(enResearch.status.running)).toBeTruthy();
  });

  it("maps awaiting/completed/terminal statuses to semantic tones without pulse", () => {
    const cases: Array<[string, string, string]> = [
      ["awaiting_user_confirm", "bg-warning", enResearch.status.awaiting_user_confirm],
      ["completed", "bg-success", enResearch.status.completed],
      ["paused", "bg-muted-foreground", enResearch.status.paused],
    ];
    for (const [status, dotClass, label] of cases) {
      const { container, unmount } = render(
        <ResearchSessionRow session={session({ status })} href="/research/s1" />,
      );
      const dot = container.querySelector("span.rounded-full.size-2");
      expect(dot?.className).toContain(dotClass);
      expect(dot?.className).not.toContain("animate-pulse");
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
      unmount();
    }
  });

  it("shows a short title without dual-writing the full goal as a subtitle", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText("Alpha market map")).toBeTruthy();
    expect(
      screen.queryByText("Map the alpha market across regions with pricing and share"),
    ).toBeNull();
  });

  it("goal chip uses brand semantic tokens (no hard-coded violet)", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const chip = screen.getByTestId("research-session-goal-chip");
    expect(chip.className).toContain("bg-brand/10");
    expect(chip.className).toContain("text-brand");
    expect(chip.className).not.toContain("violet");
    expect(chip.className).toContain("hidden");
    expect(chip.className).toContain("sm:inline-flex");
  });

  it("opens a goal dialog from the colored chip", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.queryByTestId("goal-dialog")).toBeNull();
    fireEvent.click(screen.getByTestId("research-session-goal-chip"));
    expect(screen.getByTestId("goal-dialog")).toBeTruthy();
    expect(screen.getByText(enResearch.list.goal_dialog_title)).toBeTruthy();
  });

  it("keeps stage · relative time in meta; avatars yield below sm", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText(enResearch.stage.s2_sources)).toBeTruthy();
    expect(screen.getByText("ago:2026-07-30T03:00:00Z")).toBeTruthy();
    expect(screen.getByText("Ronaldo working")).toBeTruthy();
    expect(screen.getByTestId("avatar-stack").className).toContain("hidden");
    expect(screen.getByTestId("avatar-stack").className).toContain("sm:flex");
    expect(avatarStackRef.agentIds).toEqual(["agent-1", "agent-2"]);
  });

  it("falls back to a truncated goal as the title when title is empty", () => {
    const longGoal =
      "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员，开发环境要求。目前我们的设备是几台 linux 服务器";
    const { container } = render(
      <ResearchSessionRow session={session({ title: "", goal: longGoal })} href="/research/s1" />,
    );
    const titleEl = container.querySelector(
      '[data-testid="research-session-row"] .font-medium.tracking-tight',
    );
    expect(titleEl?.textContent?.includes("…")).toBe(true);
    expect(titleEl?.textContent?.length ?? 0).toBeLessThan(longGoal.length);
  });

  it("renders the localized stage and falls back to the raw stage key", () => {
    const { rerender } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText(enResearch.stage.s2_sources)).toBeTruthy();
    rerender(<ResearchSessionRow session={session({ current_stage: "s9_unknown" })} href="/research/s1" />);
    expect(screen.getByText("s9_unknown")).toBeTruthy();
  });

  it("renders no avatar stack when the fleet preview is empty", () => {
    render(<ResearchSessionRow session={session({ fleet_preview: [] })} href="/research/s1" />);
    expect(screen.queryByTestId("avatar-stack")).toBeNull();
  });

  it("dims archived rows", () => {
    const { container } = render(
      <ResearchSessionRow session={session({ status: "archived" })} href="/research/s1" />,
    );
    expect(
      container.querySelector('[data-testid="research-session-row"]')?.className,
    ).toContain("opacity-55");
  });

  it("links title/meta to the session, with the actions menu outside the links", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const links = screen.getAllByRole("link");
    expect(links.some((a) => a.getAttribute("href") === "/research/s1")).toBe(true);
    const actions = screen.getByTestId("row-actions");
    for (const link of links) {
      expect(link.contains(actions)).toBe(false);
    }
  });
});
