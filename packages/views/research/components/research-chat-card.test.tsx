import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ResearchMessage } from "@multica/core/types";
import { ResearchChatCard } from "./research-chat-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        chat: {
          you: "You",
          process: "Process",
          system: "System",
          process_tag: "Process",
        },
      }),
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="avatar" />,
}));

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

  it("renders user chat without process styling", () => {
    const message: ResearchMessage = {
      id: "m2",
      session_id: "s1",
      sender_type: "user",
      sender_id: "u1",
      target_agent_id: null,
      body: "纠正一下方向",
      card_kind: "chat",
      meta: {},
      created_at: "2026-07-29T08:01:00Z",
    };
    const { container } = render(<ResearchChatCard message={message} members={[]} />);
    expect(screen.getByText("纠正一下方向")).toBeTruthy();
    expect(container.querySelector(".border-dashed")).toBeNull();
  });
});
