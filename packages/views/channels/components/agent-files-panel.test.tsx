// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { api } from "@multica/core/api";
import { AgentFilesPanel } from "./agent-files-panel";

vi.mock("@multica/core/api", () => ({
  api: {
    listAgentFiles: vi.fn(),
    getAgentFileContent: vi.fn(),
    updateAgentFileContent: vi.fn(),
  },
}));

vi.mock("@uiw/react-codemirror", () => ({
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (value: string) => void;
  }) => (
    <textarea
      aria-label="File content"
      value={value}
      onChange={(event) => onChange(event.currentTarget.value)}
    />
  ),
}));

const members: MemberWithUser[] = [
  {
    id: "m-owner",
    user_id: "user-owner",
    workspace_id: "ws-1",
    role: "member",
    name: "Owner",
    display_name: "Owner",
    email: "owner@example.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
];

function makeAgent(ownerId = "user-owner"): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "atlas",
    display_name: "Atlas",
    description: "Coordinates project context",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "workspace",
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: ownerId,
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };
}

function renderPanel(agent: Agent, currentUserId = "user-owner") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AgentFilesPanel agent={agent} currentUserId={currentUserId} members={members} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

describe("AgentFilesPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listAgentFiles).mockResolvedValue({
      agent_id: "agent-1",
      status: "ok",
      nodes: [{ path: "memory/MEMORY.md", is_dir: false, size: 12 }],
      truncated: false,
    });
    vi.mocked(api.getAgentFileContent).mockResolvedValue({
      content: "{\"ok\":true}",
      encoding: "",
      mime_type: "",
      content_hash: "hash-1",
      truncated: false,
      too_large: false,
      binary: false,
    });
    vi.mocked(api.updateAgentFileContent).mockResolvedValue({
      content_hash: "hash-2",
      conflict: false,
    });
  });

  it("shows file tree controls for the creating user", async () => {
    renderPanel(makeAgent());
    expect(await screen.findByText("MEMORY.md")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /show hidden files/i })).toBeInTheDocument();
    expect(api.listAgentFiles).toHaveBeenCalledWith("agent-1", { include_hidden: false });
  });

  it("shows only public information for a non-owner", async () => {
    renderPanel(makeAgent("user-owner"), "user-other");
    expect(screen.getByText("Atlas")).toBeInTheDocument();
    expect(screen.getByText(/only the creator can view/i)).toBeInTheDocument();
    await waitFor(() => expect(api.listAgentFiles).not.toHaveBeenCalled());
  });

  it("opens a file editor and saves text content", async () => {
    vi.mocked(api.listAgentFiles).mockResolvedValue({
      agent_id: "agent-1",
      status: "ok",
      nodes: [{ path: "config/settings.json", is_dir: false, size: 12 }],
      truncated: false,
    });

    renderPanel(makeAgent());
    fireEvent.click(await screen.findByText("settings.json"));

    const editor = await screen.findByLabelText("File content");
    expect(editor).toHaveValue("{\n  \"ok\": true\n}");
    fireEvent.change(editor, { target: { value: "{\n  \"ok\": false\n}" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(api.updateAgentFileContent).toHaveBeenCalledWith("agent-1", {
      path: "config/settings.json",
      content: "{\n  \"ok\": false\n}",
      expected_content_hash: "hash-1",
    }));
  });
});
