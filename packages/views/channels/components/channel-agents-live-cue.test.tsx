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
        presence_idle: `{{members}} · {{agents}}`,
        presence_live: `{{members}} · {{agents}} · {{working}} working`,
        presence_attention: `{{members}} · {{agents}} · needs attention`,
        view_members_aria: `View members`,
        working_list_title: `Working · {{count}}`,
        working_verb_with_duration: `{{verb}} · {{duration}}`,
        working_failed: `Couldn't reply · try @ again`,
        working_no_reply: `No reply · try @ again`,
        working_dismiss: `Dismiss`,
        working_dismiss_aria: `Dismiss {{name}}'s status`,
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

function member(over: Partial<ChannelMemberBrief>): ChannelMemberBrief {
  return {
    member_type: "user",
    member_id: "u1",
    name: "alice",
    display_name: "Alice",
    ...over,
  };
}

describe("ChannelAgentsLiveCue (LRM-581 Presence Cluster)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = false;
  });

  it("idle cluster shows facepile + N · M with no outer Stop", () => {
    const onOpen = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={[
          member({ member_id: "u1", display_name: "Alice" }),
          member({
            member_type: "agent",
            member_id: "a1",
            name: "beckham",
            display_name: "Beckham",
          }),
        ]}
        tasks={[]}
        onOpenMembers={onOpen}
      />,
    );
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveAttribute("data-presence-state", "idle");
    expect(screen.getByTestId("channel-presence-count")).toHaveTextContent("4 · 8");
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
    fireEvent.click(cue);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("K≥2 working shows shimmer count and Stop only inside hover card", () => {
    const onStop = vi.fn();
    const onStopAll = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={[
          member({
            member_type: "agent",
            member_id: "a1",
            name: "beckham",
            display_name: "Beckham",
          }),
          member({
            member_type: "agent",
            member_id: "a2",
            name: "wendy",
            display_name: "Wendy",
          }),
        ]}
        tasks={[
          task({ status: "running" }),
          task({
            agent_id: "a2",
            agent_name: "Wendy",
            task_id: "t2",
            inbox_event_id: "inbox-2",
            status: "queued",
          }),
        ]}
        onStopTask={onStop}
        onStopAll={onStopAll}
      />,
    );
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveAttribute("data-presence-state", "live");
    expect(screen.getByTestId("channel-presence-count")).toHaveTextContent(
      "4 · 8 · 2 working",
    );
    expect(cue.querySelector(".animate-chat-text-shimmer")).not.toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();

    // HoverCard content is portaled / may need pointer — open via focus path:
    // for unit test we render mobile popover path instead.
  });

  it("K=1 suppresses Working chrome even when tasks are running", () => {
    render(
      <ChannelAgentsLiveCue
        memberCount={2}
        agentCount={1}
        members={[
          member({
            member_type: "agent",
            member_id: "a1",
            name: "beckham",
            display_name: "Beckham",
          }),
        ]}
        tasks={[task({ status: "running" })]}
        onStopTask={vi.fn()}
        onStopAll={vi.fn()}
      />,
    );
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveAttribute("data-presence-state", "idle");
    expect(screen.getByTestId("channel-presence-count")).toHaveTextContent("2 · 1");
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
  });

  it("mobile live cluster opens Working list with Stop all footer", () => {
    mobileState.isMobile = true;
    const onStopAll = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={[]}
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
    fireEvent.click(screen.getByTestId("channel-agents-working-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });

  it("dismisses terminal rows from the mobile working list", () => {
    mobileState.isMobile = true;
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        members={[]}
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
    fireEvent.click(screen.getByTestId("channel-agents-live-cue"));
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-dismiss"));
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveAttribute(
      "data-presence-state",
      "idle",
    );
  });

  it("dm variant is always null (K=1 — no header Working chrome)", () => {
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
