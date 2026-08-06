// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
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
    workspace_role: "member",
    runtime_id: "runtime-1",
    name: "atlas",
    display_name: "Atlas",
    description: "Coordinates project context",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
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

function renderPanel(
  agent: Agent,
  currentUserId = "user-owner",
  access?: { canReadFiles?: boolean; canEditFiles?: boolean },
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AgentFilesPanel
        agent={agent}
        currentUserId={currentUserId}
        members={members}
        onClose={() => {}}
        {...access}
      />
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
    const memory = await screen.findByRole("button", { name: "memory" });
    expect(memory).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("MEMORY.md")).not.toBeInTheDocument();
    fireEvent.click(memory);
    expect(await screen.findByText("MEMORY.md")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /show hidden files/i })).toBeInTheDocument();
    expect(api.listAgentFiles).toHaveBeenCalledWith("agent-1", { include_hidden: false });
  });

  // LRM-1305 — decorative lucide inside named buttons must not dual-announce.
  it("chrome lucide icons inside named buttons are aria-hidden", async () => {
    renderPanel(makeAgent());
    const close = screen.getByRole("button", { name: "Close agent panel" });
    expect(close.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
    const hiddenToggle = screen.getByRole("button", { name: /show hidden files/i });
    expect(hiddenToggle.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });


  it("allows dev read-only access for a non-owner", async () => {
    renderPanel(makeAgent("user-owner"), "user-other", {
      canReadFiles: true,
      canEditFiles: false,
    });
    fireEvent.click(await screen.findByRole("button", { name: "memory" }));
    expect(await screen.findByText("MEMORY.md")).toBeInTheDocument();
    expect(api.listAgentFiles).toHaveBeenCalledWith("agent-1", { include_hidden: false });

    fireEvent.click(screen.getByText("MEMORY.md"));
    expect(await screen.findByLabelText("File content")).toHaveValue("{\"ok\":true}");
    expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
    expect(api.updateAgentFileContent).not.toHaveBeenCalled();
  });


  it("keeps long file names within the Files tab width", async () => {
    const longFileName = "this-is-a-very-long-file-name-that-must-not-widen-the-agent-profile-panel.md";
    vi.mocked(api.listAgentFiles).mockResolvedValue({
      agent_id: "agent-1",
      status: "ok",
      nodes: [{ path: `memory/${longFileName}`, is_dir: false, size: 12 }],
      truncated: false,
    });

    const { container } = renderPanel(makeAgent());

    fireEvent.click(await screen.findByRole("button", { name: "memory" }));
    const fileName = await screen.findByText(longFileName);
    expect(fileName).toHaveClass("min-w-0", "flex-1", "truncate");
    expect(fileName.closest("button")).toHaveClass("min-w-0", "w-full");
    expect(container.querySelector(".overflow-auto")).toHaveClass("min-w-0");
  });

  // LRM-453: DialogContent's default absolute ✕ used to stack beside the
  // header Close editor control (two X). Keep a single card close; Esc /
  // backdrop still dismiss via Dialog onOpenChange.
  it("file preview dialog exposes a single close control (no outer Dialog ✕)", async () => {
    renderPanel(makeAgent());
    fireEvent.click(await screen.findByRole("button", { name: "memory" }));
    fireEvent.click(await screen.findByText("MEMORY.md"));

    expect(await screen.findByRole("button", { name: "Close editor" })).toBeInTheDocument();
    // DialogContent's built-in close uses data-slot="dialog-close".
    expect(document.querySelectorAll('[data-slot="dialog-close"]')).toHaveLength(0);
    // While the dialog is open the panel is aria-hidden; only the card close
    // remains in the accessible tree (no second Dialog ✕).
    expect(screen.getAllByRole("button", { name: /close/i })).toHaveLength(1);
  });

});
