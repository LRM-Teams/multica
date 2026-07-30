import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ResearchSession } from "@multica/core/types";
import enResearch from "../../locales/en/research.json";

const avatarStackRef = vi.hoisted(() => ({ agentIds: undefined as readonly string[] | undefined }));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown) => fn(enResearch),
  }),
}));

vi.mock("../../i18n/use-time-ago", () => ({
  useTimeAgo: () => (dateStr: string) => `ago:${dateStr}`,
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({ children, href, className }: { children: React.ReactNode; href: string; className?: string }) => (
    <a href={href} className={className}>{children}</a>
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

import { ResearchSessionRow } from "./research-session-row";

function session(partial: Partial<ResearchSession> = {}): ResearchSession {
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
    fleet_preview: [
      { agent_id: "agent-1" },
      { agent_id: "agent-2" },
    ],
    ...partial,
  };
}

describe("ResearchSessionRow (LRM-788)", () => {
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

  it("truncates title and goal, falling back to goal when title is empty", () => {
    const { container } = render(
      <ResearchSessionRow session={session({ title: "" })} href="/research/s1" />,
    );
    const title = container.querySelector(".font-medium");
    expect(title?.className).toContain("truncate");
    expect(title?.textContent).toBe("Map the alpha market");
  });

  it("renders the localized stage chip and falls back to the raw stage key", () => {
    const { rerender } = render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByText(enResearch.stage.s2_sources)).toBeTruthy();
    rerender(<ResearchSessionRow session={session({ current_stage: "s9_unknown" })} href="/research/s1" />);
    expect(screen.getByText("s9_unknown")).toBeTruthy();
  });

  it("passes fleet preview ids to the avatar stack", () => {
    avatarStackRef.agentIds = undefined;
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    expect(screen.getByTestId("avatar-stack")).toBeTruthy();
    expect(avatarStackRef.agentIds).toEqual(["agent-1", "agent-2"]);
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

  it("makes the whole row a link to the session, with the actions menu outside", () => {
    render(<ResearchSessionRow session={session()} href="/research/s1" />);
    const link = screen.getByRole("link");
    expect(link.getAttribute("href")).toBe("/research/s1");
    expect(link.textContent).toContain("Alpha market map");
    const actions = screen.getByTestId("row-actions");
    expect(link.contains(actions)).toBe(false);
  });
});
