/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Agent, Channel, MemberWithUser, NotePage } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteShareDialog } from "./note-share-dialog";

vi.mock("@multica/core/notes/mutations", () => ({
  useUpdateNotePageShares: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
  }),
}));

function page(): NotePage {
  return {
    id: "page-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "user-owner",
    title: "Brief",
    content: "",
    sort_key: "a",
    share_user_ids: [],
    share_agent_ids: ["agent-1"],
    share_channel_ids: [],
    can_manage_shares: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    deleted_at: null,
  };
}

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "rt-1",
    name: "notes-bot",
    display_name: "笔记助手",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_status: "online",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    model: "m1",
    owner_id: "user-owner",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  };
}

function channel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: "ch-1",
    workspace_id: "ws-1",
    name: "sprint-room",
    kind: "group",
    description: null,
    lark_chat_id: null,
    created_by: "user-owner",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("NoteShareDialog", () => {
  it("shows separate agent and channel sections among virtual members", () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteShareDialog
          page={page()}
          members={
            [
              {
                id: "m-1",
                workspace_id: "ws-1",
                user_id: "user-2",
                role: "member",
                created_at: "2026-01-01T00:00:00Z",
                name: "ada",
                display_name: "Ada",
                email: "ada@example.com",
                avatar_url: null,
                description: "",
              },
            ] as MemberWithUser[]
          }
          agents={[agent()]}
          channels={[channel(), channel({ id: "dm-1", name: "dm:user:agent", kind: "dm" })]}
          workspaceName="LRM"
          open
          onOpenChange={() => {}}
        />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("note-share-agents")).toBeTruthy();
    expect(screen.getByTestId("note-share-channels")).toBeTruthy();
    expect(screen.getByText("智能体")).toBeTruthy();
    expect(screen.getByText("频道")).toBeTruthy();
    expect(screen.getByText("笔记助手")).toBeTruthy();
    expect(screen.getByText("sprint-room")).toBeTruthy();
    expect(screen.queryByText("dm:user:agent")).toBeNull();
  });
});
