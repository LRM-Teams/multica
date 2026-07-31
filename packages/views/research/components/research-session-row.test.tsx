import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchSession } from "@multica/core/types";
import enResearch from "../../locales/en/research.json";

const avatarStackRef = vi.hoisted(() => ({ agentIds: undefined as readonly string[] | undefined }));

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
  AgentAvatarStack: ({ agentIds }: { agentIds: readonly string[] }) => {
    avatarStackRef.agentIds = agentIds;
    return agentIds.length > 0 ? <span data-testid="avatar-stack" /> : null;
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

describe("ResearchSessionRow (LRM-788 / LRM-906)", () => {
  it("running status paints a brand dot with pulse and an accessible label", () => {
    const { container } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const dot = container.querySelector("span.rounded-full.size-2");
    expect(dot?.className).toContain("bg-brand");
    expect(dot?.className).toContain("animate-pulse");
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
      expect(screen.getByText(label)).toBeTruthy();
      unmount();
    }
  });

  it("shows a short title without dual-writing the full goal as a subtitle", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText("Alpha market map")).toBeTruthy();
    // Full goal must not appear as a second muted line; only via chip / dialog.
    expect(
      screen.queryByText("Map the alpha market across regions with pricing and share"),
    ).toBeNull();
    expect(screen.getByRole("button", { name: /goal ·/i })).toBeTruthy();
  });

  it("falls back to a truncated goal as the title when title is empty", () => {
    const longGoal =
      "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员，开发环境要求。目前我们的设备是几台 linux 服务器";
    render(
      <ResearchSessionRow session={session({ title: "", goal: longGoal })} href="/research/s1" />,
    );
    const titleLink = screen.getAllByRole("link")[0];
    expect(titleLink).toBeDefined();
    expect(titleLink?.textContent?.includes("…")).toBe(true);
    expect(titleLink?.textContent?.length ?? 0).toBeLessThan(longGoal.length);
  });

  it("opens a goal dialog from the colored chip", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.queryByTestId("goal-dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /goal ·/i }));
    expect(screen.getByTestId("goal-dialog")).toBeTruthy();
    expect(screen.getByText(enResearch.list.goal_dialog_title)).toBeTruthy();
    expect(
      screen.getByText("Map the alpha market across regions with pricing and share"),
    ).toBeTruthy();
  });

  it("renders stage, who, empty output, and fleet avatars", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText(enResearch.stage.s2_sources)).toBeTruthy();
    expect(screen.getByText("Ronaldo working")).toBeTruthy();
    expect(screen.getByText(enResearch.list.no_output)).toBeTruthy();
    expect(screen.getByTestId("avatar-stack")).toBeTruthy();
    expect(avatarStackRef.agentIds).toEqual(["agent-1", "agent-2"]);
  });

  it("shows handoff summary as the output line when present", () => {
    render(
      <ResearchSessionRow
        session={session({ handoff_summary: "Draft v0.3 ready" })}
        href="/research/s1"
      />,
    );
    expect(screen.getByText("Output · Draft v0.3 ready")).toBeTruthy();
  });

  it("renders the localized stage chip and falls back to the raw stage key", () => {
    const { rerender } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText(enResearch.stage.s2_sources)).toBeTruthy();
    rerender(<ResearchSessionRow session={session({ current_stage: "s9_unknown" })} href="/research/s1" />);
    expect(screen.getByText("s9_unknown")).toBeTruthy();
  });

  it("renders no avatar stack when the fleet preview is empty", () => {
    render(<ResearchSessionRow session={session({ fleet_preview: [] })} href="/research/s1" />);
    expect(screen.queryByTestId("avatar-stack")).toBeNull();
  });

  it("shows relative time from updated_at and a hover-reveal chevron", () => {
    const { container } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText("ago:2026-07-30T03:00:00Z")).toBeTruthy();
    const chevron = container.querySelector("svg.opacity-0");
    expect(chevron).toBeTruthy();
    expect(chevron?.getAttribute("class")).toContain("group-hover:opacity-100");
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
