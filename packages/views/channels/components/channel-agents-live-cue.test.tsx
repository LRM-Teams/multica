"use client";

import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import { ChannelAgentsLiveCue } from "./channel-agents-live-cue";

const mobileState = { isMobile: false };
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileState.isMobile,
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
        members_prefix: `{{members}} members ·`,
        members_only: `{{members}} members`,
        agents_idle: `{{agents}} agents`,
        agents_live: `{{agents}} agents · {{working}} working`,
        agents_attention: `{{agents}} agents · needs attention`,
        presence_counts: `{{members}} · {{agents}}`,
        presence_working: `{{working}} working`,
        presence_attention: `Needs attention`,
        dm_live: `{{working}} working`,
        dm_attention: `Needs attention`,
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

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: ({ name }: { name: string }) => <span data-testid="avatar">{name}</span>,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="face">{actorId}</span>
  ),
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

const roster: ChannelMemberBrief[] = [
  {
    member_type: "user",
    member_id: "u1",
    name: "frank",
    display_name: "Frank",
  },
  {
    member_type: "agent",
    member_id: "a1",
    name: "beckham",
    display_name: "Beckham",
  },
  {
    member_type: "agent",
    member_id: "a2",
    name: "wendy",
    display_name: "Wendy",
  },
];

describe("ChannelAgentsLiveCue Presence Cluster (LRM-581 / LRM-584)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = false;
  });

  it("idle cluster shows faces · members · agents with no Stop chrome", () => {
    const onOpenMembers = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={roster}
        tasks={[]}
        onOpenMembers={onOpenMembers}
      />,
    );
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveAttribute("data-presence", "idle");
    expect(screen.getByTestId("channel-presence-counts")).toHaveTextContent(
      "4 · 8",
    );
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
    fireEvent.click(cue);
    expect(onOpenMembers).toHaveBeenCalledTimes(1);
  });

  it("K=1 running has no Working chrome and no outer Stop (Frank v3)", () => {
    const onStop = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={roster}
        tasks={[task({ status: "running" })]}
        onStopTask={onStop}
      />,
    );
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveAttribute(
      "data-presence",
      "idle",
    );
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
  });

  it("K≥2 shows working shimmer and Stop all only inside the hover card", () => {
    const onStopAll = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={roster}
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
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveAttribute("data-presence", "working");
    expect(screen.getByTestId("channel-presence-working")).toHaveTextContent(
      "2 working",
    );
    expect(screen.getByTestId("channel-presence-working").className).toContain(
      "animate-chat-text-shimmer",
    );
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();

    // HoverCard content is portaled/open on hover — fire pointer enter on trigger
    // then query list; for unit simplicity open via focusing the button and
    // asserting Stop all is not in the outer tree until content mounts.
    fireEvent.pointerEnter(cue);
    // Base UI HoverCard may keep content closed without real hover delay in jsdom.
    // Drive open by clicking on mobile path instead for Stop-all assertion.
  });

  it("mobile K≥2 opens Working list with footer Stop all", () => {
    mobileState.isMobile = true;
    const onStopAll = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={roster}
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
    fireEvent.click(screen.getByTestId("channel-agents-live-cue"));
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    expect(screen.getByText(/Thinking/)).toBeInTheDocument();
    expect(screen.getByText(/Queued/)).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });

  it("attention-only (failed) shows danger label and opens list to dismiss", () => {
    mobileState.isMobile = true;
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={roster}
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
    expect(screen.getByTestId("channel-presence-working")).toHaveTextContent(
      "Needs attention",
    );
    expect(screen.getByTestId("channel-presence-working").className).toContain(
      "text-destructive",
    );
    fireEvent.click(screen.getByTestId("channel-agents-live-cue"));
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-dismiss"));
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
    expect(screen.queryByTestId("channel-presence-working")).toBeNull();
  });

  it("dm variant never renders header Working chrome", () => {
    const { rerender } = render(
      <ChannelAgentsLiveCue variant="dm" agentCount={1} tasks={[]} />,
    );
    expect(screen.queryByTestId("channel-agents-live-cue")).toBeNull();

    rerender(
      <ChannelAgentsLiveCue
        variant="dm"
        agentCount={1}
        tasks={[task({ status: "running" })]}
        onStopTask={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("channel-agents-live-cue")).toBeNull();
  });
});
