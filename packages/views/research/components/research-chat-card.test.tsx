import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ResearchFleetMember, ResearchMessage } from "@multica/core/types";
import { speakerMemberForMessage } from "../lib/research-chat-speaker";
import { ResearchChatCard } from "./research-chat-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        chat: {
          you: "You",
          from_you: "You",
          to_lead: "Ronaldo",
          process: "Process",
          system: "System",
          process_tag: "Process",
          stopped_tag: "Stopped",
          streaming: "Streaming…",
          streaming_from: "Fleet",
          streaming_wait: "Generating…",
          stream_settled: "Done",
        },
      }),
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="avatar" />,
}));

vi.mock("@multica/ui/markdown", () => ({
  StreamingMarkdown: ({ content }: { content: string }) => <div>{content}</div>,
}));

const lead: ResearchFleetMember = {
  id: "m-lead",
  agent_id: "agent-ronaldo",
  role: "lead",
  status: "active",
  is_lead: true,
  name: "罗纳尔多",
  display_name: "罗纳尔多",
};

describe("ResearchChatCard", () => {
  it("renders process cards with dashed style and op tag", () => {
    const message: ResearchMessage = {
      id: "m1",
      session_id: "s1",
      sender_type: "system",
      sender_id: null,
      target_agent_id: null,
      body: "调研团已就位",
      card_kind: "process",
      meta: { op: "session_kickoff" },
      created_at: "2026-07-29T08:00:00Z",
    };
    const { container } = render(<ResearchChatCard message={message} members={[]} />);
    expect(screen.getByText("调研团已就位")).toBeTruthy();
    expect(container.querySelector(".border-dashed")).toBeTruthy();
    expect(screen.getByText(/session_kickoff/)).toBeTruthy();
  });

  it("renders user chat as You even when routed to the lead", () => {
    const message: ResearchMessage = {
      id: "m2",
      session_id: "s1",
      sender_type: "user",
      sender_id: "u1",
      target_agent_id: lead.agent_id,
      body: "纠正一下方向",
      card_kind: "chat",
      meta: {},
      created_at: "2026-07-29T08:01:00Z",
    };
    const { container } = render(<ResearchChatCard message={message} members={[lead]} />);
    expect(screen.getByText("纠正一下方向")).toBeTruthy();
    expect(screen.getAllByText("You").length).toBeGreaterThan(0);
    // Speaker name must be You; lead may only appear as the route target.
    expect(container.querySelector("header .font-medium")?.textContent).toBe("You");
    expect(container.querySelector(".border-dashed")).toBeNull();
    expect(screen.getByText(/→ 罗纳尔多/)).toBeTruthy();
  });

  it("does not treat target_agent_id as the speaker", () => {
    const message: ResearchMessage = {
      id: "m3",
      session_id: "s1",
      sender_type: "user",
      sender_id: "u1",
      target_agent_id: lead.agent_id,
      body: "ping",
      card_kind: "chat",
      meta: {},
      created_at: "2026-07-29T08:02:00Z",
    };
    expect(speakerMemberForMessage(message, [lead])).toBeUndefined();
  });
});
