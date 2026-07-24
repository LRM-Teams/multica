"use client";

import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ChannelActiveTask } from "@multica/core/types";
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
        dm_live: `{{working}} working`,
        dm_attention: `Needs attention`,
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

describe("ChannelAgentsLiveCue (LRM-581)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mobileState.isMobile = false;
  });

  it("idle channel roster is plain agents text with no live chrome", () => {
    render(
      <ChannelAgentsLiveCue memberCount={4} agentCount={8} tasks={[]} />,
    );
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveTextContent("8 agents");
    expect(cue.tagName).toBe("SPAN");
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.getByTestId("channel-roster-summary")).toHaveTextContent(
      "4 members ·",
    );
  });

  it("running tasks change the cue and expose Stop next to it", () => {
    const onStop = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        tasks={[task({ status: "running" })]}
        onStopTask={onStop}
      />,
    );
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveTextContent("8 agents · 1 working");
    expect(cue.querySelector(".animate-chat-text-shimmer")).not.toBeNull();
    fireEvent.click(screen.getByTestId("channel-agents-cue-stop"));
    expect(onStop).toHaveBeenCalledTimes(1);
  });

  it("shows danger attention cue for failed/no_reply without silent blank", () => {
    render(
      <ChannelAgentsLiveCue
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
    const cue = screen.getByTestId("channel-agents-live-cue");
    expect(cue).toHaveTextContent("8 agents · needs attention");
    expect(cue.querySelector(".text-destructive")).not.toBeNull();
  });

  it("dismisses terminal rows from the mobile working list", () => {
    mobileState.isMobile = true;
    render(
      <ChannelAgentsLiveCue
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
    fireEvent.click(screen.getByTestId("channel-agents-live-cue"));
    expect(screen.getByTestId("channel-agents-working-list")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("channel-agents-working-dismiss"));
    expect(screen.queryByTestId("channel-agents-working-list")).toBeNull();
    // Idle again — no live chrome.
    expect(screen.getByTestId("channel-agents-live-cue").tagName).toBe("SPAN");
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveTextContent(
      "8 agents",
    );
  });

  it("dm variant is idle-null and live-compact", () => {
    const { rerender } = render(
      <ChannelAgentsLiveCue variant="dm" agentCount={1} tasks={[]} />,
    );
    expect(screen.queryByTestId("channel-roster-summary")).toBeNull();

    rerender(
      <ChannelAgentsLiveCue
        variant="dm"
        agentCount={1}
        tasks={[task({ status: "running" })]}
        onStopTask={vi.fn()}
      />,
    );
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveTextContent(
      "1 working",
    );
  });

  it("dm variant with canStop=false keeps status and hides outer Stop (LRM-589)", () => {
    render(
      <ChannelAgentsLiveCue
        variant="dm"
        agentCount={1}
        tasks={[task({ status: "running" })]}
        canStop={false}
      />,
    );
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveTextContent(
      "1 working",
    );
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
  });

  it("Stop all appears for multiple stoppable tasks", () => {
    const onStopAll = vi.fn();
    render(
      <ChannelAgentsLiveCue
        memberCount={2}
        agentCount={3}
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
    fireEvent.click(screen.getByTestId("channel-agents-cue-stop-all"));
    expect(onStopAll).toHaveBeenCalledTimes(1);
  });
});
