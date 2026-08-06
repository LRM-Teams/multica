import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, ChatSession } from "@multica/core/types";
import enChat from "../../locales/en/chat.json";

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId, showStatusDot }: { actorId: string; showStatusDot?: boolean }) => (
    <span
      data-testid={`avatar-${actorId}`}
      data-status-dot={showStatusDot ? "true" : "false"}
    />
  ),
}));

// Time pulls the viewing timezone from the auth store — stub it so this test
// stays focused on row selection/sorting, not timestamp formatting.
vi.mock("../../i18n/time", () => ({
  Time: ({ value }: { kind: string; value: string }) => <span>{value}</span>,
}));

import { ChatContactList } from "./chat-contact-list";

const TEST_RESOURCES = { en: { chat: enChat } };

function makeAgent(over: Partial<Agent> & Pick<Agent, "id" | "name">): Agent {
  return {
    workspace_id: "ws-1",
    owner_id: "user-1",
    runtime_id: "runtime-1",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "sonnet",
    skills: [],
    created_at: new Date(0).toISOString(),
    updated_at: new Date(0).toISOString(),
    archived_at: null,
    archived_by: null,
    ...over,
    display_name: over.display_name ?? over.name,
  } as Agent;
}

function makeSession(over: Partial<ChatSession> & Pick<ChatSession, "id" | "agent_id" | "updated_at">): ChatSession {
  return {
    workspace_id: "ws-1",
    creator_id: "user-1",
    title: "",
    status: "active",
    has_unread: false,
    created_at: over.updated_at,
    project_id: null,
    ...over,
  } as ChatSession;
}

const agents = [
  makeAgent({ id: "a-alpha", name: "Alpha" }),
  makeAgent({ id: "a-beta", name: "Beta" }),
  makeAgent({ id: "a-old", name: "Archived", archived_at: new Date(1).toISOString() }),
];

function renderList(sessions: ChatSession[], onSelect = vi.fn()) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ChatContactList
        sessions={sessions}
        agents={agents}
        activeAgentId={null}
        onSelect={onSelect}
      />
    </I18nProvider>,
  );
  return onSelect;
}

describe("ChatContactList", () => {
  it("shows one row per agent, most-recent first", () => {
    renderList([
      makeSession({ id: "s-alpha", agent_id: "a-alpha", updated_at: "2026-01-01T00:00:00Z" }),
      makeSession({ id: "s-beta", agent_id: "a-beta", updated_at: "2026-06-01T00:00:00Z" }),
    ]);
    const rows = screen.getAllByRole("button");
    expect(rows).toHaveLength(2);
    // Beta updated more recently → listed first.
    expect(rows[0]).toHaveTextContent("Beta");
    expect(rows[1]).toHaveTextContent("Alpha");
  });

  it("clicking a contact selects the agent's most-recent session", () => {
    const onSelect = renderList([
      makeSession({ id: "s-old", agent_id: "a-alpha", updated_at: "2026-01-01T00:00:00Z" }),
      makeSession({ id: "s-new", agent_id: "a-alpha", updated_at: "2026-06-01T00:00:00Z" }),
    ]);
    fireEvent.click(screen.getByRole("button", { name: /Alpha/ }));
    expect(onSelect).toHaveBeenCalledWith("a-alpha", "s-new");
  });

  it("excludes archived agents and archived sessions", () => {
    renderList([
      makeSession({ id: "s-arch-agent", agent_id: "a-old", updated_at: "2026-06-01T00:00:00Z" }),
      makeSession({ id: "s-arch", agent_id: "a-alpha", updated_at: "2026-06-01T00:00:00Z", status: "archived" }),
      makeSession({ id: "s-ok", agent_id: "a-beta", updated_at: "2026-05-01T00:00:00Z" }),
    ]);
    const rows = screen.getAllByRole("button");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveTextContent("Beta");
  });

  it("renders the empty state when there are no usable sessions", () => {
    renderList([]);
    expect(screen.getByText(enChat.contacts.empty)).toBeInTheDocument();
  });

  it("uses the shared presence dot and keeps unread separate from the avatar", () => {
    renderList([
      makeSession({
        id: "s-alpha",
        agent_id: "a-alpha",
        updated_at: "2026-06-01T00:00:00Z",
        has_unread: true,
      }),
    ]);

    expect(screen.getByTestId("avatar-a-alpha")).toHaveAttribute("data-status-dot", "true");
    const unread = screen.getByTestId("contact-unread-a-alpha");
    expect(unread).toHaveAccessibleName(enChat.window.unread);
    expect(unread.parentElement).not.toContainElement(screen.getByTestId("avatar-a-alpha"));
  });
});
