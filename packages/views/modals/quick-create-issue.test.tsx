import { forwardRef, useImperativeHandle, useRef, useState, type ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const mockQuickCreateIssue = vi.hoisted(() => vi.fn());
const mockSetLastActor = vi.hoisted(() => vi.fn());
const mockSetLastProjectId = vi.hoisted(() => vi.fn());
const mockSetPrompt = vi.hoisted(() => vi.fn());
const mockClearPrompt = vi.hoisted(() => vi.fn());
const mockSetKeepOpen = vi.hoisted(() => vi.fn());
const mockSetLastMode = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockUploadWithToast = vi.hoisted(() => vi.fn());

const mockQuickCreateStore = {
  lastActorType: null as "agent" | null,
  lastActorId: null as string | null,
  setLastActor: mockSetLastActor,
  lastProjectId: null as string | null,
  setLastProjectId: mockSetLastProjectId,
  prompt: "Persisted draft prompt",
  setPrompt: mockSetPrompt,
  clearPrompt: mockClearPrompt,
  keepOpen: false,
  setKeepOpen: mockSetKeepOpen,
};

// Per-test override for the projects query, so tests can swap between
// "loaded as empty" (the deleted-project case) and "still loading" without
// re-mocking the whole module.
const mockProjectsQuery = vi.hoisted(() => ({
  data: [] as Array<{ id: string; title: string; icon: string | null }>,
  isSuccess: true,
}));


// Agents are mutable per-test so a case can hand an agent whose display_name
// differs from its routing handle (the #433 bug: the picker rendered the raw
// `agent_f0…` handle instead of the display name).
const mockAgentsData = vi.hoisted(
  () => ({
    list: [
      { id: "agent-1", name: "Bohan", archived_at: null, runtime_id: "runtime-1" },
    ] as Array<{
      id: string;
      name: string;
      display_name?: string;
      archived_at: string | null;
      runtime_id: string;
    }>,
  }),
);

vi.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: string[] }) => {
    // Workspace-scoped query keys carry the wsId as `queryKey[1]`; the
    // discriminator is at `queryKey[2]` (e.g. ["workspaces", wsId, "agents"]).
    switch (queryKey[0]) {
      case "members":
        return { data: [{ user_id: "user-1", role: "admin" }] };
      case "agents":
        return { data: mockAgentsData.list };
      case "runtimes":
        return { data: [{ id: "runtime-1", metadata: { cli_version: "1.2.3" } }] };
      case "projects":
        return mockProjectsQuery;
      default:
        return { data: [] };
    }
  },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    quickCreateIssue: mockQuickCreateIssue,
  },
  ApiError: class ApiError extends Error {
    body?: unknown;
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ name: "Test Workspace" }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"] }),
}));

vi.mock("@multica/core/issues/stores/quick-create-store", () => ({
  useQuickCreateStore: (selector?: (state: typeof mockQuickCreateStore) => unknown) =>
    (selector ? selector(mockQuickCreateStore) : mockQuickCreateStore),
}));

vi.mock("@multica/core/issues/stores/create-mode-store", () => ({
  useCreateModeStore: (selector?: (state: { setLastMode: typeof mockSetLastMode }) => unknown) =>
    (selector ? selector({ setLastMode: mockSetLastMode }) : { setLastMode: mockSetLastMode }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector?: (state: { user: { id: string } }) => unknown) =>
    (selector ? selector({ user: { id: "user-1" } }) : { user: { id: "user-1" } }),
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
  checkQuickCreateCliVersion: () => ({ state: "ok", min: "1.0.0" }),
  readRuntimeCliVersion: () => "1.2.3",
  MIN_QUICK_CREATE_CLI_VERSION: "1.0.0",
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: mockUploadWithToast, uploading: false }),
}));

vi.mock("../issues/components/pickers/assignee-picker", () => ({
  canAssignAgent: () => true,
}));

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

vi.mock("../issues/components", () => ({
  PriorityPicker: () => <div data-testid="priority-picker" />,
  DueDatePicker: () => <div data-testid="due-date-picker" />,
}));

vi.mock("../projects/components/project-picker", () => ({
  ProjectPicker: () => <div data-testid="project-picker" />,
}));

vi.mock("../common/pill-button", () => ({
  PillButton: () => <div data-testid="pill-button" />,
}));

vi.mock("../editor", () => {
  const ContentEditor = forwardRef(({ defaultValue, onUpdate, onSubmit, onUploadFile, placeholder }: any, ref: any) => {
    const valueRef = useRef(defaultValue || "");
    const [value, setValue] = useState(defaultValue || "");

    useImperativeHandle(ref, () => ({
      getMarkdown: () => valueRef.current,
      clearContent: () => {
        valueRef.current = "";
        setValue("");
      },
      uploadFile: vi.fn(),
      focus: vi.fn(),
    }));

    return (
      <>
        <textarea
          value={value}
          placeholder={placeholder}
          onChange={(e) => {
            valueRef.current = e.target.value;
            setValue(e.target.value);
            onUpdate?.(e.target.value);
          }}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
              onSubmit?.();
            }
          }}
        />
        <button
          type="button"
          onClick={() => onUploadFile?.(new File(["image"], "shot.png", { type: "image/png" }))}
        >
          Mock editor upload
        </button>
      </>
    );
  });
  ContentEditor.displayName = "ContentEditor";

  return {
    ContentEditor,
    useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
    FileDropOverlay: () => null,
  };
});

vi.mock("@multica/ui/components/ui/dialog", () => ({
  DialogTitle: ({ children, className }: { children: ReactNode; className?: string }) => (
    <div className={className}>{children}</div>
  ),
}));

vi.mock("../issues/components/pickers/property-picker", () => ({
  PropertyPicker: ({
    trigger,
    children,
    searchPlaceholder,
    onSearchChange,
  }: {
    trigger: ReactNode;
    children: ReactNode;
    searchPlaceholder?: string;
    onSearchChange?: (v: string) => void;
  }) => (
    <>
      {trigger}
      <input
        aria-label="actor-search"
        placeholder={searchPlaceholder}
        onChange={(e) => onSearchChange?.(e.target.value)}
      />
      {children}
    </>
  ),
  PickerItem: ({
    children,
    onClick,
    selected,
  }: {
    children: ReactNode;
    onClick: () => void;
    selected?: boolean;
  }) => (
    <button type="button" onClick={onClick} data-selected={selected ? "true" : "false"}>
      {children}
    </button>
  ),
  PickerSection: ({ label, children }: { label: string; children: ReactNode }) => (
    <div>
      <div data-testid="picker-section-label">{label}</div>
      {children}
    </div>
  ),
  PickerEmpty: () => <div data-testid="picker-empty" />,
}));

vi.mock("@multica/ui/components/ui/button", () => ({
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => (
    <button type="button" disabled={disabled} onClick={onClick}>
      {children}
    </button>
  ),
}));

vi.mock("@multica/ui/components/ui/switch", () => ({
  Switch: ({ checked, onCheckedChange }: { checked: boolean; onCheckedChange: (v: boolean) => void }) => (
    <input
      aria-label="Create another"
      type="checkbox"
      checked={checked}
      onChange={(e) => onCheckedChange(e.target.checked)}
    />
  ),
}));

vi.mock("@multica/ui/components/common/file-upload-button", () => ({
  FileUploadButton: () => <button type="button">Upload file</button>,
}));

vi.mock("sonner", () => ({
  toast: {
    success: mockToastSuccess,
  },
}));

import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enModals from "../locales/en/modals.json";
import { AgentCreatePanel } from "./quick-create-issue";

const TEST_RESOURCES = { en: { common: enCommon, modals: enModals } };

function renderPanel(props: React.ComponentProps<typeof AgentCreatePanel>) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AgentCreatePanel {...props} />
    </I18nProvider>,
  );
}

describe("AgentCreatePanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockQuickCreateStore.lastActorType = null;
    mockQuickCreateStore.lastActorId = null;
    mockQuickCreateStore.lastProjectId = null;
    mockQuickCreateStore.prompt = "Persisted draft prompt";
    mockQuickCreateStore.keepOpen = false;
    mockProjectsQuery.data = [];
    mockProjectsQuery.isSuccess = true;
    mockAgentsData.list = [
      { id: "agent-1", name: "Bohan", archived_at: null, runtime_id: "runtime-1" },
    ];
    mockQuickCreateIssue.mockResolvedValue(undefined);
    mockUploadWithToast.mockResolvedValue({
      id: "019ec09d-6222-722b-bdfa-427b105d80be",
      workspace_id: "ws-test",
      issue_id: null,
      comment_id: null,
      chat_session_id: null,
      chat_message_id: null,
      uploader_type: "member",
      uploader_id: "user-1",
      filename: "shot.png",
      url: "/uploads/shot.png",
      download_url: "/api/attachments/019ec09d-6222-722b-bdfa-427b105d80be/download",
      markdown_url: "/api/attachments/019ec09d-6222-722b-bdfa-427b105d80be/download",
      content_type: "image/png",
      size_bytes: 5,
      created_at: "2026-06-12T00:00:00Z",
    });
    mockSetKeepOpen.mockImplementation((value: boolean) => {
      mockQuickCreateStore.keepOpen = value;
    });
  });

  it("loads the persisted prompt draft when no transient prompt is provided", () => {
    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    expect(
      screen.getByPlaceholderText(
        'Tell the agent what to do, e.g. "let Bohan fix the inbox loading slowness in the Web project"',
      ),
    ).toHaveValue("Persisted draft prompt");
  });

  it("writes prompt changes back to the draft store and clears them after submit", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    renderPanel({ onClose, isExpanded: false, setIsExpanded: vi.fn() });

    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the inbox loading slowness in the Web project"',
    );

    await user.clear(editor);
    await user.type(editor, "New agent prompt");
    expect(mockSetPrompt).toHaveBeenLastCalledWith("New agent prompt");

    await user.click(screen.getByRole("button", { name: /^Create \(/i }));

    await waitFor(() => {
      expect(mockQuickCreateIssue).toHaveBeenCalledWith({
        agent_id: "agent-1",
        prompt: "New agent prompt",
        project_id: undefined,
      });
    });

    expect(mockSetLastActor).toHaveBeenCalledWith("agent", "agent-1");
    // No project picked → persisted project preference is cleared so the
    // store stays in sync with the actual outgoing request.
    expect(mockSetLastProjectId).toHaveBeenCalledWith(null);
    expect(mockClearPrompt).toHaveBeenCalled();
    expect(mockSetLastMode).toHaveBeenCalledWith("agent");
    expect(onClose).toHaveBeenCalled();
  });

  it("passes referenced upload attachment ids to quick-create", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    renderPanel({ onClose, isExpanded: false, setIsExpanded: vi.fn() });

    await user.click(screen.getByRole("button", { name: "Mock editor upload" }));
    await waitFor(() => expect(mockUploadWithToast).toHaveBeenCalled());

    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the inbox loading slowness in the Web project"',
    );
    await user.clear(editor);
    fireEvent.change(editor, {
      target: {
        value: "Create issue with ![image](/api/attachments/019ec09d-6222-722b-bdfa-427b105d80be/download)",
      },
    });

    await user.click(screen.getByRole("button", { name: /^Create \(/i }));

    await waitFor(() => {
      expect(mockQuickCreateIssue).toHaveBeenCalledWith({
        agent_id: "agent-1",
        prompt: "Create issue with ![image](/api/attachments/019ec09d-6222-722b-bdfa-427b105d80be/download)",
        project_id: undefined,
        parent_issue_id: undefined,
        attachment_ids: ["019ec09d-6222-722b-bdfa-427b105d80be"],
      });
    });
  });

  it("forwards source chat context from the carry payload to quick-create", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    renderPanel({
      onClose,
      isExpanded: false,
      setIsExpanded: vi.fn(),
      data: {
        source: {
          channel_id: "channel-1",
          message_id: "message-1",
          thread_root_message_id: "root-1",
        },
      },
    });

    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the inbox loading slowness in the Web project"',
    );
    await user.clear(editor);
    await user.type(editor, "Investigate the regression");

    await user.click(screen.getByRole("button", { name: /^Create \(/i }));

    await waitFor(() => {
      expect(mockQuickCreateIssue).toHaveBeenCalledWith({
        agent_id: "agent-1",
        prompt: "Investigate the regression",
        project_id: undefined,
        parent_issue_id: undefined,
        source: {
          channel_id: "channel-1",
          message_id: "message-1",
          thread_root_message_id: "root-1",
        },
      });
    });
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  // #433: the agent picker rendered the raw routing handle (agent_f0…) instead
  // of the human display name. It now reuses the shared identity row, so the
  // display name is primary and search matches on it.
  it("shows the agent display name in the picker, not the raw routing handle", async () => {
    mockAgentsData.list = [
      {
        id: "agent-9",
        name: "agent_f049f0f2",
        display_name: "Wendy",
        archived_at: null,
        runtime_id: "runtime-1",
      },
    ];

    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    // The human display name is rendered — before #433 only the raw handle showed.
    expect(screen.getAllByText("Wendy").length).toBeGreaterThan(0);

    // Search matches on the display name too, not only the handle.
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("actor-search"), "wendy");
    expect(screen.getAllByText("Wendy").length).toBeGreaterThan(0);
    expect(screen.queryByTestId("picker-empty")).toBeNull();
  });

  // If the user's persisted `lastProjectId` points at a project that has
  // been deleted (or moved to another workspace), the modal must not keep
  // submitting that dead UUID. Once the projects query resolves and the id
  // is missing, we clear BOTH local state and the persisted preference;
  // dropping only local state would leave the next open re-seeding the same
  // dead value and trigger the server's `project not found` rejection.
  it("clears a stale persisted project once the projects list resolves without it", async () => {
    mockQuickCreateStore.lastProjectId = "deleted-proj";
    mockProjectsQuery.data = [];
    mockProjectsQuery.isSuccess = true;

    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    await waitFor(() => {
      expect(mockSetLastProjectId).toHaveBeenCalledWith(null);
    });
  });

  // Mirror case: while the query is still loading, we must NOT preemptively
  // clear the persisted preference — that would wipe a perfectly valid
  // selection on every open before the list ever renders.
  it("keeps the persisted project while the projects list is still loading", () => {
    mockQuickCreateStore.lastProjectId = "proj-1";
    mockProjectsQuery.data = [];
    mockProjectsQuery.isSuccess = false;

    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    expect(mockSetLastProjectId).not.toHaveBeenCalled();
  });

  // When the modal was opened from "Add sub issue" on an existing issue,
  // the manual panel transfers parent_issue_id through the `data` payload
  // on switch-to-agent. The agent panel must forward that UUID to the
  // quick-create API silently — without surfacing a parent picker — so the
  // new issue is filed as a sub-issue. Dropping parent_issue_id here was
  // the original bug; this locks the wiring in.
  it("forwards parent_issue_id from the carry payload to the quick-create API", async () => {
    const user = userEvent.setup();

    renderPanel({
      onClose: vi.fn(),
      isExpanded: false,
      setIsExpanded: vi.fn(),
      data: {
        parent_issue_id: "parent-uuid-1",
        parent_issue_identifier: "MUL-2534",
      },
    });

    // Sub-issue context chip is visible so the user knows the new issue
    // will be filed as a sub-issue.
    expect(screen.getByTestId("agent-sub-issue-chip")).toBeInTheDocument();

    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the inbox loading slowness in the Web project"',
    );
    await user.clear(editor);
    await user.type(editor, "Investigate the regression");

    await user.click(screen.getByRole("button", { name: /^Create \(/i }));

    await waitFor(() => {
      expect(mockQuickCreateIssue).toHaveBeenCalledWith({
        agent_id: "agent-1",
        prompt: "Investigate the regression",
        project_id: undefined,
        parent_issue_id: "parent-uuid-1",
      });
    });
  });

  // The sub-issue chip is purely opt-in context — it only appears when the
  // modal was opened from an "Add sub issue" entry. A plain quick-create
  // (no parent in data) must NOT render the chip; otherwise users would see
  // a stray badge on every quick-create.
  it("does not render the sub-issue chip when no parent is seeded", () => {
    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });
    expect(screen.queryByTestId("agent-sub-issue-chip")).toBeNull();
  });
});
