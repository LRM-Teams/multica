"use client";

import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import { ChannelPresenceCluster } from "./channel-agents-live-cue";

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
      id === "a1" ? "Beckham" : id === "a2" ? "Wendy" : "Agent",
    getActorInitials: () => "A",
    getActorAvatarUrl: () => null,
  }),
}));

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
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid={`face-${actorId}`}>{actorId}</span>
  ),
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: ({ name }: { name: string }) => <span data-testid="avatar">{name}</span>,
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

function members(ids: string[]): ChannelMemberBrief[] {
  return ids.map((id) => ({
    member_type: id.startsWith("u") ? ("user" as const) : ("agent" as const),
    member_id: id,
    name: id,
    display_name: id,
    avatar_url: null,
  }));
}

describe("ChannelPresenceCluster (LRM-581 A v3)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = false;
  });

  it("idle K≥2 shows faces · N · M with no working chrome or outer Stop", () => {
    const onOpen = vi.fn();
    render(
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
    expect(screen.getByTestId("channel-presence-counts")).toHaveTextContent(
      "4 · 8",
    );
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
    fireEvent.click(chip);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("K=1 never shows Working chrome even with running tasks", () => {
    render(
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

  it("K≥2 working shows shimmer and no outer Stop; Stop all only in card", () => {
    const onStopAll = vi.fn();
    mobileState.isMobile = true;
    render(
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
    const working = screen.getByTestId("channel-presence-working");
    expect(working).toHaveTextContent("2 working");
    expect(working.className).toContain("animate-chat-text-shimmer");
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();

    fireEvent.click(chip);
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });

  it("dismisses terminal rows from the mobile working list", () => {
    mobileState.isMobile = true;
    render(
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
            outcome: "failed",
          }),
        ]}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-header-members-chip"));
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-dismiss"));
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
    expect(screen.getByTestId("channel-header-members-chip")).toHaveAttribute(
      "data-presence-working",
      "false",
    );
  });
});
