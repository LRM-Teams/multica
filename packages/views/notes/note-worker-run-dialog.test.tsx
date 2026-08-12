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
  },
}));

vi.mock("@multica/core/identity", () => ({
  resolveActorDisplayName: (actor: { name?: string }, fallback: string) => actor.name || fallback,
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
    createNoteWorkerJob.mockResolvedValue(dispatchedJob());
  });

  it("dispatches a Worker job and never creates an Editor ai-job", async () => {
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
  });
});
