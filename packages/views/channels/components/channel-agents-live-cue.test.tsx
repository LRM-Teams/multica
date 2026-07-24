"use client";

import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelActiveTask } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelAgentsLiveCue } from "./channel-agents-live-cue";

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) => (id === "a1" ? "Beckham" : "Frontend"),
    getActorInitials: () => "B",
    getActorAvatarUrl: () => null,
  }),
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: ({ name }: { name: string }) => <span data-testid="avatar">{name}</span>,
}));

function renderCue(ui: React.ReactElement) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      {ui}
    </I18nProvider>,
  );
}

function task(
  partial: Partial<ChannelActiveTask> & Pick<ChannelActiveTask, "task_id" | "agent_id">,
): ChannelActiveTask {
  return {
    agent_name: partial.agent_name ?? "Agent",
    status: partial.status ?? "running",
    kind: partial.kind ?? "reply",
    inbox_event_id: partial.inbox_event_id ?? `inbox-${partial.task_id}`,
    ...partial,
  };
}

describe("ChannelAgentsLiveCue (LRM-581)", () => {
  it("idle roster shows plain agents label without Stop", () => {
    renderCue(<ChannelAgentsLiveCue memberCount={4} agentCount={8} tasks={[]} />);
    expect(screen.getByTestId("channel-roster-summary")).toHaveTextContent(
      "4 members · 8 agents",
    );
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
    expect(screen.queryByTestId("channel-agents-cue-stop-all")).toBeNull();
  });

  it("live cue shows working count + adjacent Stop for a single task", () => {
    const onStopTask = vi.fn();
    renderCue(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        tasks={[
          task({
            task_id: "t1",
            agent_id: "a1",
            agent_name: "Beckham",
            status: "running",
          }),
        ]}
        onStopTask={onStopTask}
        onStopAll={vi.fn()}
      />,
    );
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveTextContent(
      "8 agents · 1 working",
    );
    fireEvent.click(screen.getByTestId("channel-agents-cue-stop"));
    expect(onStopTask).toHaveBeenCalledWith(expect.objectContaining({ task_id: "t1" }));
  });

  it("multi task shows Stop all beside cue (not only in hover)", () => {
    const onStopAll = vi.fn();
    renderCue(
      <ChannelAgentsLiveCue
        memberCount={4}
        agentCount={8}
        tasks={[
          task({ task_id: "t1", agent_id: "a1", status: "running" }),
          task({ task_id: "t2", agent_id: "a2", status: "queued" }),
        ]}
        onStopTask={vi.fn()}
        onStopAll={onStopAll}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-agents-cue-stop-all"));
    expect(onStopAll).toHaveBeenCalled();
  });

  it("DM agentsOnly idle renders nothing", () => {
    const { container } = renderCue(
      <ChannelAgentsLiveCue agentsOnly agentCount={1} tasks={[]} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("terminal-only cue uses attention copy without adjacent Stop", () => {
    renderCue(
      <ChannelAgentsLiveCue
        memberCount={2}
        agentCount={3}
        tasks={[
          task({
            task_id: "done-1",
            agent_id: "a2",
            agent_name: "Wendy",
            status: "no_reply",
            outcome: "no_reply",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    expect(screen.getByTestId("channel-agents-live-cue")).toHaveTextContent(
      "needs attention",
    );
    expect(screen.queryByTestId("channel-agents-cue-stop")).toBeNull();
  });
});
