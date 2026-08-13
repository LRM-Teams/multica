/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, NoteWorkerJob } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteWorkerRunDialog } from "./note-worker-run-dialog";

const createNoteWorkerJob = vi.fn();
const createNoteAIJob = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    createNoteWorkerJob: (...args: unknown[]) => createNoteWorkerJob(...args),
    createNoteAIJob: (...args: unknown[]) => createNoteAIJob(...args),
    listChannels: vi.fn(async () => [
      { id: "ch-1", name: "general", kind: "group", workspace_id: "ws-1" },
    ]),
    listChannelMembers: vi.fn(async () => [
      { member_type: "agent", member_id: "agent-1", name: "Deepseek" },
    ]),
  },
}));

vi.mock("@multica/core/identity", () => ({
  resolveActorDisplayName: (actor: { name?: string }, fallback: string) => actor.name || fallback,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const openNoteWorkerChat = vi.fn();
vi.mock("./use-open-note-worker-chat", () => ({
  useOpenNoteWorkerChat: () => ({ openNoteWorkerChat }),
}));

const agents: Agent[] = [
  {
    id: "agent-1",
    workspace_id: "ws-1",
    name: "Deepseek",
    description: "",
    avatar_url: null,
    runtime_id: null,
    owner_id: "user-1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
  } as Agent,
];

function dispatchedJob(overrides: Partial<NoteWorkerJob> = {}): NoteWorkerJob {
  return {
    id: "job-1",
    workspace_id: "ws-1",
    page_id: "page-1",
    agent_id: "agent-1",
    instruction: "Create issues from this brief",
    status: "dispatched",
    intent: "worker",
    task_id: "task-1",
    channel_id: "dm-1",
    channel_message_id: "msg-1",
    chat_session_id: null,
    failure_reason: null,
    created_at: "2026-08-12T00:00:00Z",
    updated_at: "2026-08-12T00:00:00Z",
    ...overrides,
  };
}

function renderDialog(onDispatched = vi.fn()) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return {
    onDispatched,
    ...renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteWorkerRunDialog
          pageId="page-1"
          agents={agents}
          defaultAgentId="agent-1"
          open
          onOpenChange={vi.fn()}
          onDispatched={onDispatched}
        />
      </QueryClientProvider>,
    ),
  };
}

describe("NoteWorkerRunDialog", () => {
  beforeEach(() => {
    createNoteWorkerJob.mockReset();
    createNoteAIJob.mockReset();
    openNoteWorkerChat.mockReset();
    createNoteWorkerJob.mockResolvedValue(dispatchedJob());
  });

  it("dispatches a Worker job to the agent DM by default", async () => {
    const user = userEvent.setup();
    const { onDispatched } = renderDialog();

    await user.type(
      screen.getByPlaceholderText(/Create issues from this brief/i),
      "Create issues from this brief",
    );
    await user.click(screen.getByRole("button", { name: /Start|开始/i }));

    await waitFor(() => {
      expect(createNoteWorkerJob).toHaveBeenCalledWith("page-1", {
        agent_id: "agent-1",
        instruction: "Create issues from this brief",
        intent: "worker",
      });
    });
    expect(createNoteAIJob).not.toHaveBeenCalled();
    expect(onDispatched).toHaveBeenCalledWith(expect.objectContaining({ id: "job-1", intent: "worker" }));
    expect(openNoteWorkerChat).toHaveBeenCalledWith(expect.objectContaining({ channel_id: "dm-1" }));
  });

  it("dispatches into a selected channel with its agent", async () => {
    const user = userEvent.setup();
    createNoteWorkerJob.mockResolvedValue(dispatchedJob({ channel_id: "ch-1" }));
    renderDialog();

    await user.click(screen.getByRole("button", { name: /Channel|频道/i }));
    await waitFor(() => {
      expect(screen.getByText("general")).toBeTruthy();
    });
    await user.click(screen.getByText("general"));
    await user.type(
      screen.getByPlaceholderText(/Create issues from this brief/i),
      "Summarize for the channel",
    );
    await user.click(screen.getByRole("button", { name: /Start|开始/i }));

    await waitFor(() => {
      expect(createNoteWorkerJob).toHaveBeenCalledWith("page-1", {
        agent_id: "agent-1",
        instruction: "Summarize for the channel",
        intent: "worker",
        channel_id: "ch-1",
      });
    });
  });

  it("submits on Enter and keeps Shift+Enter as a newline", async () => {
    const user = userEvent.setup();
    renderDialog();
    const input = screen.getByPlaceholderText(/Create issues from this brief/i);

    await user.type(input, "Line one");
    await user.keyboard("{Shift>}{Enter}{/Shift}");
    await user.type(input, "Line two");
    expect(createNoteWorkerJob).not.toHaveBeenCalled();

    await user.keyboard("{Enter}");
    await waitFor(() => {
      expect(createNoteWorkerJob).toHaveBeenCalledWith("page-1", {
        agent_id: "agent-1",
        instruction: "Line one\nLine two",
        intent: "worker",
      });
    });
  });
});
