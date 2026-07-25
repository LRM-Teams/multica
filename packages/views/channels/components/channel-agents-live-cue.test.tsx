"use client";

import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import {
  ChannelPresenceCluster,
} from "./channel-agents-live-cue";

const mobileState = { isMobile: false };
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../../agents/use-agent-live-status", () => ({
  useAgentActivityProjection: () => null,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) =>
      id === "a1" ? "Beckham" : id === "a2" ? "Wendy" : "Unknown Agent",
    getActorInitials: () => "A",
    getActorAvatarUrl: () => null,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberProfileOptions: (_wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", "ws-1", "member-profiles", type, id],
    queryFn: async () => {
      if (id === "a-hidden") {
        return {
          member_type: "agent",
          member_id: "a-hidden",
          name: "hidden-slug",
          display_name: "隐藏群管",
          avatar_url: "/agent-avatars/hidden.png",
        };
      }
      if (id === "a-face") {
        return {
          member_type: "agent",
          member_id: "a-face",
          name: "face-slug",
          display_name: "有脸Agent",
          avatar_url: "/agent-avatars/face.png",
        };
      }
      throw new Error(`profile missing: ${type}/${id}`);
    },
    enabled: !!id,
  }),
}));

function renderWithQuery(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>,
  );
}

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (picker: (ns: Record<string, unknown>) => unknown, vars?: Record<string, unknown>) => {
      const header = {
        presence_counts: `{{members}} · {{agents}}`,
        presence_working: `{{working}} working`,
        presence_attention: `Needs attention`,
        working_list_title: `Working · {{count}}`,
        working_verb_with_duration: `{{verb}} · {{duration}}`,
        working_failed: `Couldn't reply · try @ again`,
        working_no_reply: `No reply · try @ again`,
        working_dismiss: `Dismiss`,
        working_dismiss_aria: `Dismiss {{name}}'s status`,
        view_members_aria: `View members`,
      };
      const agent_status = {
        running: "Thinking",
        queued: "Queued",
        stop: "Stop",
        stop_all: "Stop all",
        stop_aria: `Stop {{name}}'s current task`,
        stop_all_aria: `Stop all {{count}} current agent tasks`,
      };
      const ns = { header, agent_status };
      const template = picker(ns as never);
      if (typeof template !== "string") return String(template);
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(vars?.[key] ?? `{{${key}}}`),
      );
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({
    actorId,
    showStatusDot,
    avatarUrlHint,
    name,
  }: {
    actorId: string;
    showStatusDot?: boolean;
    avatarUrlHint?: string | null;
    name?: string;
  }) => (
    <span
      data-testid={`face-${actorId}`}
      data-show-status-dot={showStatusDot ? "true" : "false"}
      data-avatar-hint={avatarUrlHint ?? ""}
      data-name={name ?? ""}
    >
      {actorId}
    </span>
  ),
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: ({ name }: { name: string }) => <span data-testid="avatar">{name}</span>,
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

function task(over: Partial<ChannelActiveTask>): ChannelActiveTask {
  return {
    agent_id: "a1",
    agent_name: "Beckham",
    task_id: "t1",
    status: "running",
    kind: "reply",
    reason: "mention",
    inbox_event_id: "inbox-1",
    ...over,
  };
}

function members(
  ids: string[],
  extras?: Record<string, Partial<ChannelMemberBrief>>,
): ChannelMemberBrief[] {
  return ids.map((id) => ({
    member_type: id.startsWith("u") ? ("user" as const) : ("agent" as const),
    member_id: id,
    name: id,
    display_name: id,
    avatar_url: null,
    ...extras?.[id],
  }));
}

describe("ChannelPresenceCluster (LRM-581 A v3)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = false;
  });

  it("idle K≥2 shows faces only — no outer N · M counts or Stop", () => {
    const onOpen = vi.fn();
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[]}
        onOpenMembers={onOpen}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "false");
    expect(screen.getByTestId("channel-presence-faces")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-presence-counts")).toBeNull();
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(chip).not.toHaveTextContent("4");
    expect(chip).not.toHaveTextContent("8");
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
    fireEvent.click(chip);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("K=1 never shows Working chrome even with running tasks", () => {
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1"])}
        memberCount={2}
        agentCount={1}
        tasks={[task({ status: "running" })]}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "false");
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
  });

  it("K≥2 working: faces + ring only (no outer count/working text); Stop all in card", () => {
    const onStopAll = vi.fn();
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "i2",
            status: "queued",
          }),
        ]}
        onStopTask={vi.fn()}
        onStopAll={onStopAll}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    expect(screen.getByTestId("channel-presence-faces")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-presence-counts")).toBeNull();
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(chip).not.toHaveTextContent("working");
    expect(chip).not.toHaveTextContent("4");
    expect(chip).not.toHaveTextContent("8");
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();

    fireEvent.click(chip);
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });

  it("terminal no_reply/failed alone do not open Working chrome (Activity SoT)", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "inbox-2",
            status: "failed",
            outcome: "no_reply",
          }),
        ]}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "false");
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    fireEvent.click(chip);
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
  });

  it("facepile stacks with z-index and no status-dot punch-outs", () => {
    const { container } = renderWithQuery(
      <ChannelPresenceCluster
        members={members(["a1", "a2", "u1"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "i2",
            status: "running",
          }),
        ]}
      />,
    );
    const faces = screen.getByTestId("channel-presence-faces");
    expect(faces.className).toContain("isolate");
    const stacked = faces.querySelectorAll(":scope > span");
    expect(stacked.length).toBeGreaterThanOrEqual(2);
    expect((stacked[0] as HTMLElement).style.zIndex).toBe("1");
    expect((stacked[1] as HTMLElement).style.zIndex).toBe("2");
    // Facepile must not enable status-dot punch-outs (collide with neighbor rings).
    expect(
      container.querySelectorAll('[data-show-status-dot="true"]').length,
    ).toBe(0);
    expect(
      container.querySelectorAll('[data-show-status-dot="false"]').length,
    ).toBeGreaterThanOrEqual(2);
  });

  it("LRM-391: directory-miss Unknown Agent rows stay out of Working list", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a2"])}
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", inbox_event_id: "i1", status: "running" }),
          task({
            agent_id: "a-gone",
            agent_name: "Unknown Agent",
            task_id: "t-gone",
            inbox_event_id: "i-gone",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    fireEvent.click(chip);
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    expect(screen.getAllByText("Beckham").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Unknown Agent")).toBeNull();
    expect(screen.getByText("Working · 1")).toBeInTheDocument();
    expect(screen.getAllByTestId("channel-agents-working-row")).toHaveLength(1);
  });

  it("LRM-391: directory-miss resolves via member-profile into Working list", async () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1", "a-hidden"], {
          // Roster sentinel must not block profile (AC#5 still uses profile face).
          "a-hidden": {
            display_name: "Unknown Agent",
            name: "Unknown Agent",
            avatar_url: null,
          },
        })}
        memberCount={3}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-hidden",
            agent_name: "Unknown Agent",
            task_id: "t-h",
            inbox_event_id: "i-h",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    await waitFor(() => {
      expect(screen.getByTestId("channel-header-members-chip")).toHaveAttribute(
        "data-presence-working",
        "true",
      );
    });
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    await waitFor(() => {
      expect(screen.getAllByText("隐藏群管").length).toBeGreaterThanOrEqual(1);
    });
    expect(screen.queryByText("Unknown Agent")).toBeNull();
    await waitFor(() => {
      expect(
        screen
          .getAllByTestId("face-a-hidden")
          .some(
            (el) =>
              el.getAttribute("data-avatar-hint") ===
              "/agent-avatars/hidden.png",
          ),
      ).toBe(true);
    });
  });

  it("LRM-391: emit-time agent_name wins over directory Unknown Agent sentinel", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a1"])}
        memberCount={2}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-emit",
            agent_name: "前端工程师",
            task_id: "t-e",
            inbox_event_id: "i-e",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    expect(screen.getAllByText("前端工程师").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Unknown Agent")).toBeNull();
  });

  it("LRM-391 AC#5: channel roster name+avatar keeps Working face (no over-omit)", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1", "a-roster"], {
          "a-roster": {
            display_name: "群内Agent",
            avatar_url: "/agent-avatars/roster.png",
          },
        })}
        memberCount={2}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-roster",
            agent_name: "Unknown Agent",
            task_id: "t-r",
            inbox_event_id: "i-r",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    expect(screen.getByTestId("face-a-roster")).toHaveAttribute(
      "data-avatar-hint",
      "/agent-avatars/roster.png",
    );
    fireEvent.click(chip);
    expect(screen.getAllByText("群内Agent").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Unknown Agent")).toBeNull();
    // Working row Avatar also gets the roster face hint.
    const workingFace = screen.getAllByTestId("face-a-roster");
    expect(
      workingFace.some(
        (el) => el.getAttribute("data-avatar-hint") === "/agent-avatars/roster.png",
      ),
    ).toBe(true);
  });

  it("LRM-391 AC#5: profile fills Working avatar when directory has name but no face", async () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1"])}
        memberCount={1}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-face",
            agent_name: "Unknown Agent",
            task_id: "t-f",
            inbox_event_id: "i-f",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    await waitFor(() => {
      expect(screen.getByTestId("channel-header-members-chip")).toHaveAttribute(
        "data-presence-working",
        "true",
      );
    });
    await waitFor(() => {
      expect(screen.getByTestId("face-a-face")).toHaveAttribute(
        "data-avatar-hint",
        "/agent-avatars/face.png",
      );
    });
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    await waitFor(() => {
      expect(screen.getAllByText("有脸Agent").length).toBeGreaterThanOrEqual(1);
    });
  });

  it("LRM-391 AC#5: emit-time task.avatar_url seeds facepile without roster/profile", () => {
    mobileState.isMobile = true;
    renderWithQuery(
      <ChannelPresenceCluster
        members={members(["u1"])}
        memberCount={1}
        agentCount={2}
        tasks={[
          task({
            agent_id: "a-snap",
            agent_name: "快照Agent",
            avatar_url: "/agent-avatars/snap.png",
            task_id: "t-s",
            inbox_event_id: "i-s",
            status: "running",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    const chip = screen.getByTestId("channel-header-members-chip");
    expect(chip).toHaveAttribute("data-presence-working", "true");
    expect(screen.getByTestId("face-a-snap")).toHaveAttribute(
      "data-avatar-hint",
      "/agent-avatars/snap.png",
    );
    fireEvent.click(chip);
    expect(screen.getAllByText("快照Agent").length).toBeGreaterThanOrEqual(1);
    expect(
      screen
        .getAllByTestId("face-a-snap")
        .some(
          (el) =>
            el.getAttribute("data-avatar-hint") === "/agent-avatars/snap.png",
        ),
    ).toBe(true);
  });
});
