/**
 * @vitest-environment happy-dom
 */
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteAssistantBubble } from "./note-assistant-bubble";

const {
  listAgents,
  listRuntimes,
  listMembers,
  listChatSessions,
  listPendingChatTasks,
  ensureNotesAssistantAgent,
  ensurePeriodBriefCollectors,
  createNotePeriodBrief,
  getActiveNotePeriodBrief,
  toggleNoteBubble,
  setNoteBubbleOpenPageId,
  setNoteBubbleActiveSession,
} = vi.hoisted(() => ({
  listAgents: vi.fn(),
  listRuntimes: vi.fn(),
  listMembers: vi.fn(),
  listChatSessions: vi.fn(),
  listPendingChatTasks: vi.fn(),
  ensureNotesAssistantAgent: vi.fn(),
  ensurePeriodBriefCollectors: vi.fn(),
  createNotePeriodBrief: vi.fn(),
  getActiveNotePeriodBrief: vi.fn(),
  toggleNoteBubble: vi.fn(),
  setNoteBubbleOpenPageId: vi.fn(),
  setNoteBubbleActiveSession: vi.fn(),
}));

const chatState = {
  noteBubbleOpenPageId: null as string | null,
  noteBubbleActiveSessionByPage: {} as Record<string, string>,
  toggleNoteBubble,
  setNoteBubbleOpenPageId,
  setNoteBubbleActiveSession,
};

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: (...args: unknown[]) => listAgents(...args),
    listRuntimes: (...args: unknown[]) => listRuntimes(...args),
    listMembers: (...args: unknown[]) => listMembers(...args),
    listChatSessions: (...args: unknown[]) => listChatSessions(...args),
    listPendingChatTasks: (...args: unknown[]) => listPendingChatTasks(...args),
    ensureNotesAssistantAgent: (...args: unknown[]) => ensureNotesAssistantAgent(...args),
    ensurePeriodBriefCollectors: (...args: unknown[]) => ensurePeriodBriefCollectors(...args),
    createNotePeriodBrief: (...args: unknown[]) => createNotePeriodBrief(...args),
    getActiveNotePeriodBrief: (...args: unknown[]) => getActiveNotePeriodBrief(...args),
    getAgentTemplate: () => Promise.resolve(null),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (sel: (s: { user: { id: string; timezone: string } | null }) => unknown) =>
      sel({ user: { id: "user-1", timezone: "UTC" } }),
    { getState: () => ({ user: { id: "user-1", timezone: "UTC" } }) },
  ),
}));

vi.mock("@multica/core/chat", () => ({
  useChatStore: Object.assign(
    (sel: (s: typeof chatState) => unknown) => sel(chatState),
    { getState: () => chatState },
  ),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    members: () => "/members",
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("./note-assistant-fab-cluster", () => ({
  NoteAssistantFabCluster: ({
    onAction,
  }: {
    onAction: (action: "period_brief" | "highlights" | "chat") => void;
  }) => (
    <button type="button" onClick={() => onAction("period_brief")}>
      open-period
    </button>
  ),
}));

vi.mock("../chat/components/chat-window", () => ({
  ChatWindow: ({
    composerAccessory,
    onSendOverride,
    onSendIntercept,
    layout,
  }: {
    composerAccessory?: ReactNode;
    onSendOverride?: (text: string) => boolean | Promise<boolean>;
    onSendIntercept?: (text: string) => boolean;
    layout?: string;
  }) => (
    <div>
      <div data-testid="chat-window" data-layout={layout}>
        {composerAccessory}
      </div>
      <button
        type="button"
        onClick={() => {
          if (onSendIntercept?.("帮我写汇报")) return;
          void onSendOverride?.("帮我写汇报");
        }}
      >
        send-intent
      </button>
      <button type="button" onClick={() => void onSendOverride?.("只采集 Cloud Box")}>
        send-override
      </button>
    </div>
  ),
}));

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "notes-1",
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "runtime-1",
    name: "notes-assistant",
    display_name: "笔记助手",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_status: "online",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "m1",
    owner_id: "user-1",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  };
}

describe("NoteAssistantBubble period brief", () => {
  beforeEach(() => {
    listAgents.mockReset();
    listRuntimes.mockReset();
    listMembers.mockReset();
    listChatSessions.mockReset();
    listPendingChatTasks.mockReset();
    ensureNotesAssistantAgent.mockReset();
    ensurePeriodBriefCollectors.mockReset();
    createNotePeriodBrief.mockReset();
    getActiveNotePeriodBrief.mockReset();
    toggleNoteBubble.mockReset();
    setNoteBubbleOpenPageId.mockReset();
    setNoteBubbleActiveSession.mockReset();
    chatState.noteBubbleOpenPageId = null;
    chatState.noteBubbleActiveSessionByPage = {};
    getActiveNotePeriodBrief.mockResolvedValue({ run: null });
    const collectorA = agent({
      id: "collector-a",
      name: "period-collect-laptopa",
      display_name: "采集 · Laptop A",
    });
    const collectorB = agent({
      id: "collector-b",
      name: "period-collect-cloud01",
      display_name: "采集 · 云端 · Cloud Box",
      runtime_id: "runtime-cloud",
      runtime_mode: "cloud",
    });
    listAgents.mockResolvedValue([agent(), collectorA, collectorB]);
    listRuntimes.mockResolvedValue([
      { id: "runtime-1", status: "online", runtime_mode: "local", owner_id: "user-1" },
      { id: "runtime-cloud", status: "online", runtime_mode: "cloud", owner_id: "user-1" },
    ]);
    listMembers.mockResolvedValue([]);
    listChatSessions.mockResolvedValue([]);
    listPendingChatTasks.mockResolvedValue({ tasks: [] });
    ensureNotesAssistantAgent.mockResolvedValue({
      created: false,
      needs_setup: false,
      onboarding_available: false,
      setup_hint: false,
      agent: agent(),
    });
    ensurePeriodBriefCollectors.mockResolvedValue({
      agents: [collectorA, collectorB],
      created: [],
    });
    createNotePeriodBrief.mockResolvedValue({
      page: { id: "page-brief", title: "工作介绍 本周 · 底稿" },
      job: { id: "job-1", agent_id: "notes-1", channel_id: "dm-1" },
      window: { kind: "week", timezone: "UTC", start: "", end: "", label: "本周" },
      sources_used: [],
      sources_empty: [],
      sources_skipped: [],
      fact_count: 2,
      collector_agent_ids: ["collector-b"],
      chat_session_id: "session-brief",
    });
  });

  it("opens the bubble composer instead of a dialog and stays there after send", async () => {
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await user.click(screen.getByRole("button", { name: "open-period" }));
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    expect(screen.queryByRole("dialog")).toBeNull();

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    });
    await user.click(screen.getByRole("button", { name: "send-override" }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          agent_id: "notes-1",
          collector_agent_ids: ["collector-b"],
          focus: "只采集 Cloud Box",
          context_note_page_id: "page-1",
        }),
      );
    });
    expect(setNoteBubbleActiveSession).toHaveBeenCalledWith("page-1", "session-brief");
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("opens the satellite chips when the user asks for a Period Brief in chat", async () => {
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    await user.click(screen.getByRole("button", { name: "send-intent" }));
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    expect(createNotePeriodBrief).not.toHaveBeenCalled();

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    });
    await user.click(screen.getByRole("button", { name: "send-override" }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          context_note_page_id: "page-1",
          collector_agent_ids: ["collector-b"],
        }),
      );
      expect(createNotePeriodBrief.mock.calls[0]?.[0]).not.toHaveProperty("chat_session_id");
    });
  });

  it("opens as a right-side full-height sidebar on desktop", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
    );
    expect(screen.getByTestId("chat-window")).toHaveAttribute("data-layout", "sidebar");
  });

  it("closes the rail when leaving the note that opened it", () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    chatState.noteBubbleOpenPageId = "page-1";
    const view = renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note A" />
      </QueryClientProvider>,
    );
    view.rerender(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-2" pageTitle="Note B" />
      </QueryClientProvider>,
    );
    expect(setNoteBubbleOpenPageId).toHaveBeenCalledWith(null);
  });
});
