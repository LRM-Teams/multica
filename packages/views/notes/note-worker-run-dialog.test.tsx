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
    workspace_role: "member",
    runtime_id: "runtime-1",
    name: "Deepseek",
    display_name: "Deepseek",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: "user-1",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  },
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
    await user.click(screen.getByRole("button", { name: /^Start$/i }));

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
    await user.click(screen.getByRole("button", { name: /^Start$/i }));

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

  it("fills a coordination playbook and prefers a channel destination", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("button", { name: /Start collaboration/i }));

    const input = screen.getByPlaceholderText(
      /Create issues from this brief/i,
    ) as HTMLTextAreaElement;
    expect(input.value).toMatch(/coordination channel/i);
    expect(input.value).toMatch(/assign/i);
    expect(screen.getByText("general")).toBeTruthy();
    expect(screen.getByText(/works best in a group channel/i)).toBeTruthy();
  });

  it("fills hire and writeback playbooks with stable keywords", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("button", { name: /Ask Wendy to hire/i }));
    let input = screen.getByPlaceholderText(
      /Create issues from this brief/i,
    ) as HTMLTextAreaElement;
    expect(input.value).toMatch(/Wendy/i);
    expect(input.value).toMatch(/hiring proposal/i);
    expect(input.value).toMatch(/do not create agents yourself/i);

    await user.click(screen.getByRole("button", { name: /Prepare writeback/i }));
    input = screen.getByPlaceholderText(
      /Create issues from this brief/i,
    ) as HTMLTextAreaElement;
    expect(input.value).toMatch(/pending writeback/i);
    expect(input.value).toMatch(/do not edit the note body directly/i);
  });

  it("fills period_brief with reporting keywords and keeps Agent DM", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(screen.getByRole("button", { name: /Period work brief/i }));
    const input = screen.getByPlaceholderText(
      /Create issues from this brief/i,
    ) as HTMLTextAreaElement;
    expect(input.value).toMatch(/nested sub-points/i);
    expect(input.value).toMatch(/Mermaid|do not drop diagrams/i);
    expect(input.value).toMatch(/filesystem path/i);
    expect(input.value).toMatch(/Unscoped machine work|本机未归类/i);
    expect(input.value).toMatch(/Do not list raw commits/i);
    expect(input.value).not.toMatch(/PPT|slide deck copy for leadership/i);
    expect(screen.queryByText(/works best in a group channel/i)).toBeNull();
    expect(screen.getByRole("button", { name: /Agent DM/i })).toBeTruthy();
  });

  it("hides the channel hint after a channel is selected; still dispatches Worker only", async () => {
    const user = userEvent.setup();
    createNoteWorkerJob.mockResolvedValue(dispatchedJob({ channel_id: "ch-1" }));
    renderDialog();

    await user.click(screen.getByRole("button", { name: /Start collaboration/i }));
    expect(screen.getByText(/works best in a group channel/i)).toBeTruthy();

    await waitFor(() => {
      expect(screen.getByText("general")).toBeTruthy();
    });
    await user.click(screen.getByText("general"));
    expect(screen.queryByText(/works best in a group channel/i)).toBeNull();

    await user.click(screen.getByRole("button", { name: /^Start$/i }));
    await waitFor(() => {
      expect(createNoteWorkerJob).toHaveBeenCalledWith(
        "page-1",
        expect.objectContaining({
          agent_id: "agent-1",
          intent: "worker",
          channel_id: "ch-1",
        }),
      );
    });
    const [, body] = createNoteWorkerJob.mock.calls.at(-1) as [string, { instruction: string }];
    expect(body.instruction).toMatch(/coordination channel/i);
    expect(createNoteAIJob).not.toHaveBeenCalled();
  });
});
