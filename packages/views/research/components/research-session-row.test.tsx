import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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

describe("ResearchSessionRow (LRM-1106 / LRM-1099)", () => {
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

  it("shows title without dual-writing the full goal as a subtitle", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText("Alpha market map")).toBeTruthy();
    expect(
      screen.queryByText("Map the alpha market across regions with pricing and share"),
    ).toBeNull();
  });

  it("LRM-1106 D2: never renders an inline goal chip", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.queryByTestId("research-session-goal-chip")).toBeNull();
  });

  it("keeps stage energy · list Time; avatars yield below md; lead name without 进行中 suffix", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getAllByTestId("research-session-stage-energy").length).toBeGreaterThan(0);
    expect(screen.getAllByText(enResearch.stage.s2_sources).length).toBeGreaterThan(0);
    expect(screen.getAllByTestId("list-time")[0]?.textContent).toContain(
      "list:2026-07-30T03:00:00Z",
    );
    expect(screen.getByText("Ronaldo")).toBeTruthy();
    expect(screen.queryByText(/Ronaldo working/)).toBeNull();
    expect(screen.getByTestId("avatar-stack").className).toContain("hidden");
    expect(screen.getByTestId("avatar-stack").className).toContain("md:flex");
    expect(avatarStackRef.agentIds).toEqual(["agent-1", "agent-2"]);
  });

  it("LRM-1285: energy badge uses resolveStageStepState for running S2", () => {
    const { container } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const badge = container.querySelector(
      '[data-testid="research-session-stage-energy"].hidden.md\\:inline-flex, [data-testid="research-session-stage-energy"]',
    );
    // Prefer desktop instance when both exist; assert segment states on first badge.
    const first = screen.getAllByTestId("research-session-stage-energy")[0];
    const states = [...(first?.querySelectorAll("[data-stage-state]") ?? [])].map((el) =>
      el.getAttribute("data-stage-state"),
    );
    expect(states).toEqual(["done", "current", "upcoming", "upcoming"]);
    expect(badge).toBeTruthy();
  });

  it("LRM-1285 Gate FAIL: desktop stage-energy slot is fixed w-28 sibling of flex-1 title", () => {
    const longTitle =
      "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员，开发环境要求";
    const { container } = render(
      <>
        <ResearchSessionRow session={session({ title: "行业调研" })} href="/research/a" />
        <ResearchSessionRow
          session={session({ id: "s2", title: longTitle, current_stage: "s4_delivery" })}
          href="/research/b"
        />
      </>,
    );
    const rows = [...container.querySelectorAll('[data-testid="research-session-row"]')];
    expect(rows).toHaveLength(2);
    for (const row of rows) {
      const titleCol = row.querySelector(":scope > .min-w-0.flex-1");
      const slot = row.querySelector('[data-testid="research-session-stage-energy-slot"]');
      expect(titleCol).toBeTruthy();
      expect(slot).toBeTruthy();
      expect(slot?.className).toMatch(/\bw-28\b/);
      expect(slot?.className).toMatch(/\bshrink-0\b/);
      expect(slot?.className).toMatch(/\bmd:block\b/);
      // Slot is a direct flex child — not nested under the title column.
      expect(slot?.parentElement).toBe(row);
      expect(titleCol?.contains(slot!)).toBe(false);
    }
  });

  it("uses CSS truncate for long empty-title goals (no hard char ellipsis)", () => {
    const longGoal =
      "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员，开发环境要求。目前我们的设备是几台 linux 服务器";
    const { container } = render(
      <ResearchSessionRow session={session({ title: "", goal: longGoal })} href="/research/s1" />,
    );
    const titleEl = container.querySelector(
      '[data-testid="research-session-row"] .font-medium.tracking-tight',
    );
    expect(titleEl?.textContent).toBe(longGoal);
    expect(titleEl?.className).toMatch(/line-clamp-2|truncate/);
  });

  it("exposes focus-within visibility for row actions", () => {
    const { container } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const actionsWrap = screen.getByTestId("row-actions").parentElement;
    expect(actionsWrap?.className).toContain("md:group-focus-within:opacity-100");
    expect(
      container.querySelector('[data-testid="research-session-row"]')?.className,
    ).toContain("focus-within:bg-accent/70");
  });

  it("renders the localized stage and falls back to the raw stage key", () => {
    const { rerender } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getAllByText(enResearch.stage.s2_sources).length).toBeGreaterThan(0);
    rerender(<ResearchSessionRow session={session({ current_stage: "s9_unknown" })} href="/research/s1" />);
    expect(screen.getAllByText("s9_unknown").length).toBeGreaterThan(0);
  });

  it("renders no avatar stack when the fleet preview is empty", () => {
    render(<ResearchSessionRow session={session({ fleet_preview: [] })} href="/research/s1" />);
    expect(screen.queryByTestId("avatar-stack")).toBeNull();
  });

  it("softens archived rows with solid muted title — no row opacity-* (LRM-1368)", () => {
    const { container } = render(
      <ResearchSessionRow session={session({ status: "archived" })} href="/research/s1" />,
    );
    const row = container.querySelector('[data-testid="research-session-row"]');
    expect(row?.className).not.toMatch(/\bopacity-\d/);
    const title = row?.querySelector("a .text-sm.font-medium");
    expect(title?.className).toContain("text-muted-foreground");
    expect(title?.className).not.toMatch(/\btext-foreground\b/);
    expect(title?.className).not.toMatch(/\bopacity-\d/);
  });

  it("keeps non-archived titles on solid foreground (LRM-1368)", () => {
    const { container } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const title = container.querySelector(
      '[data-testid="research-session-row"] a .text-sm.font-medium',
    );
    expect(title?.className).toContain("text-foreground");
    expect(title?.className).not.toContain("text-muted-foreground");
  });

  it("links title to the session with a single primary tab stop; actions stay outside", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]?.getAttribute("href")).toBe("/research/s1");
    const actions = screen.getByTestId("row-actions");
    expect(links[0]?.contains(actions)).toBe(false);
  });

  it("calls onNavigate before leaving for detail (D-IX persist hook)", () => {
    const onNavigate = vi.fn();
    render(
      <ResearchSessionRow session={session()} href="/research/s1" onNavigate={onNavigate} />,
    );
    screen.getByRole("link").click();
    expect(onNavigate).toHaveBeenCalledTimes(1);
  });
});
