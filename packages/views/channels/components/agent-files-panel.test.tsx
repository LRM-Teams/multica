// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { api } from "@multica/core/api";
import { copyText } from "@multica/ui/lib/clipboard";
import { AgentFilesPanel } from "./agent-files-panel";

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: vi.fn(),
}));

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

const AGENT_ROOT_PATH =
  "/Users/frank/.multica/workspaces/ws-1/agents/agent-1";

function mockOneLevelFiles(
  dirs: Record<string, Array<{ path: string; is_dir: boolean; size?: number }>>,
) {
  vi.mocked(api.listAgentFiles).mockImplementation(
    async (_id, params?: { include_hidden?: boolean; path?: string }) => ({
      agent_id: "agent-1",
      status: "ok",
      nodes: dirs[params?.path ?? ""] ?? [],
      truncated: false,
      root_path: AGENT_ROOT_PATH,
    }),
  );
}

describe("AgentFilesPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockOneLevelFiles({
      "": [{ path: "memory", is_dir: true }],
      memory: [{ path: "memory/MEMORY.md", is_dir: false, size: 12 }],
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
    expect(api.listAgentFiles).toHaveBeenCalledTimes(1);
    expect(api.listAgentFiles).toHaveBeenCalledWith("agent-1", { include_hidden: false });
    fireEvent.click(memory);
    expect(await screen.findByText("MEMORY.md")).toBeInTheDocument();
    expect(api.listAgentFiles).toHaveBeenCalledWith("agent-1", { include_hidden: false, path: "memory" });
    expect(screen.getByRole("button", { name: /show hidden files/i })).toBeInTheDocument();
  });

  it("does not paint a whole-tree truncation banner on the Files tab", async () => {
    renderPanel(makeAgent());
    await screen.findByRole("button", { name: "memory" });
    expect(screen.queryByText("File list truncated.")).not.toBeInTheDocument();
  });

  it("shows the daemon agent root and copies it", async () => {
    vi.mocked(copyText).mockResolvedValue(true);
    renderPanel(makeAgent());
    expect(await screen.findByText(AGENT_ROOT_PATH)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Copy path" }));
    await waitFor(() => {
      expect(copyText).toHaveBeenCalledWith(AGENT_ROOT_PATH);
    });
  });

  it("refresh re-lists the current directories without a full-tree fetch", async () => {
    renderPanel(makeAgent());
    fireEvent.click(await screen.findByRole("button", { name: "memory" }));
    await screen.findByText("MEMORY.md");
    const before = vi.mocked(api.listAgentFiles).mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => {
      expect(vi.mocked(api.listAgentFiles).mock.calls.length).toBeGreaterThan(before);
    });
    const afterExpand = vi.mocked(api.listAgentFiles).mock.calls.slice(before);
    expect(afterExpand.some((call) => call[1]?.path === undefined || call[1]?.path === "")).toBe(true);
    expect(afterExpand.some((call) => call[1]?.path === "memory")).toBe(true);
    expect(afterExpand.every((call) => call[1]?.path !== "memory/MEMORY.md")).toBe(true);
  });

  // LRM-1305 — decorative lucide inside named buttons must not dual-announce.
  it("chrome lucide icons inside named buttons are aria-hidden", async () => {
    renderPanel(makeAgent());
    const close = screen.getByRole("button", { name: "Close agent panel" });
    expect(close.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
    const hiddenToggle = screen.getByRole("button", { name: /show hidden files/i });
    expect(hiddenToggle.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });

  it("shows only public information for a non-owner", async () => {
    renderPanel(makeAgent("user-owner"), "user-other");
    expect(screen.getByText("Atlas")).toBeInTheDocument();
    expect(screen.getByText(/only the creator can view/i)).toBeInTheDocument();
    await waitFor(() => expect(api.listAgentFiles).not.toHaveBeenCalled());
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

  it("opens a file editor and saves text content", async () => {
    mockOneLevelFiles({
      "": [{ path: "config", is_dir: true }],
      config: [{ path: "config/settings.json", is_dir: false, size: 12 }],
    });

    renderPanel(makeAgent());
    fireEvent.click(await screen.findByRole("button", { name: "config" }));
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

  it("keeps long file names within the Files tab width", async () => {
    const longFileName = "this-is-a-very-long-file-name-that-must-not-widen-the-agent-profile-panel.md";
    mockOneLevelFiles({
      "": [{ path: "memory", is_dir: true }],
      memory: [{ path: `memory/${longFileName}`, is_dir: false, size: 12 }],
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

  it("closes the file preview via header Close editor, Escape, and backdrop", async () => {
    renderPanel(makeAgent());
    fireEvent.click(await screen.findByRole("button", { name: "memory" }));
    fireEvent.click(await screen.findByText("MEMORY.md"));

    const closeEditor = await screen.findByRole("button", { name: "Close editor" });
    fireEvent.click(closeEditor);
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Close editor" })).not.toBeInTheDocument();
    });

    fireEvent.click(await screen.findByText("MEMORY.md"));
    expect(await screen.findByRole("button", { name: "Close editor" })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Close editor" })).not.toBeInTheDocument();
    });

    fireEvent.click(await screen.findByText("MEMORY.md"));
    expect(await screen.findByRole("button", { name: "Close editor" })).toBeInTheDocument();
    const backdrop = document.querySelector('[data-slot="dialog-overlay"]');
    expect(backdrop).not.toBeNull();
    fireEvent.click(backdrop!);
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Close editor" })).not.toBeInTheDocument();
    });
  });
});
