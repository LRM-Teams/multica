/**
 * @vitest-environment happy-dom
 */
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatKeys, chatMessagesOptions } from "@multica/core/chat/queries";
import { noteKeys } from "@multica/core/notes/queries";
import type { Agent } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteAssistantBubble } from "./note-assistant-bubble";

const {
  listAgents,
  listRuntimes,
  listMembers,
  listChatSessions,
  listChatMessages,
  listPendingChatTasks,
  ensureNotesAssistantAgent,
  ensurePeriodBriefCollectors,
  createNotePeriodBrief,
  cancelNotePeriodBrief,
  deleteNotePeriodBriefPlan,
  getActiveNotePeriodBrief,
  getNotePeriodBriefPlan,
  putNotePeriodBriefPlan,
  toggleNoteBubble,
  setNoteBubbleOpenPageId,
  setNoteBubbleActiveSession,
  setNoteSelectionQuote,
  removeNoteSelectionExcerpt,
  lastOutgoing,
} = vi.hoisted(() => ({
  listAgents: vi.fn(),
  listRuntimes: vi.fn(),
  listMembers: vi.fn(),
  listChatSessions: vi.fn(),
  listChatMessages: vi.fn(),
  listPendingChatTasks: vi.fn(),
  ensureNotesAssistantAgent: vi.fn(),
  ensurePeriodBriefCollectors: vi.fn(),
  createNotePeriodBrief: vi.fn(),
  cancelNotePeriodBrief: vi.fn(),
  deleteNotePeriodBriefPlan: vi.fn(),
  getActiveNotePeriodBrief: vi.fn(),
  getNotePeriodBriefPlan: vi.fn(),
  putNotePeriodBriefPlan: vi.fn(),
  toggleNoteBubble: vi.fn(),
  setNoteBubbleOpenPageId: vi.fn(),
  setNoteBubbleActiveSession: vi.fn(),
  setNoteSelectionQuote: vi.fn(),
  removeNoteSelectionExcerpt: vi.fn(),
  lastOutgoing: { text: "" },
}));

const chatState = {
  noteBubbleOpenPageId: null as string | null,
  noteBubbleActiveSessionByPage: {} as Record<string, string>,
  inputDrafts: {} as Record<string, string>,
  noteSelectionQuote: null as {
    pageId: string;
    excerpts: { id: string; text: string }[];
    askedAt: number;
  } | null,
  toggleNoteBubble,
  setNoteBubbleOpenPageId,
  setNoteBubbleActiveSession,
  setNoteSelectionQuote,
  removeNoteSelectionExcerpt,
};

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: (...args: unknown[]) => listAgents(...args),
    listRuntimes: (...args: unknown[]) => listRuntimes(...args),
    listMembers: (...args: unknown[]) => listMembers(...args),
    listChatSessions: (...args: unknown[]) => listChatSessions(...args),
    listChatMessages: (...args: unknown[]) => listChatMessages(...args),
    listPendingChatTasks: (...args: unknown[]) => listPendingChatTasks(...args),
    ensureNotesAssistantAgent: (...args: unknown[]) => ensureNotesAssistantAgent(...args),
    ensurePeriodBriefCollectors: (...args: unknown[]) => ensurePeriodBriefCollectors(...args),
    createNotePeriodBrief: (...args: unknown[]) => createNotePeriodBrief(...args),
    cancelNotePeriodBrief: (...args: unknown[]) => cancelNotePeriodBrief(...args),
    deleteNotePeriodBriefPlan: (...args: unknown[]) => deleteNotePeriodBriefPlan(...args),
    getActiveNotePeriodBrief: (...args: unknown[]) => getActiveNotePeriodBrief(...args),
    getNotePeriodBriefPlan: (...args: unknown[]) => getNotePeriodBriefPlan(...args),
    putNotePeriodBriefPlan: (...args: unknown[]) => putNotePeriodBriefPlan(...args),
    getAgentTemplate: () => Promise.resolve(null),
    listComputers: () => Promise.resolve([]),
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
  DRAFT_NEW_SESSION: "__new__",
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

vi.mock("../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    getShareableUrl: (path: string) => path,
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: vi.fn(),
}));

vi.mock("./note-assistant-fab-cluster", () => ({
  NoteAssistantFabCluster: ({
    onOpen,
    isRunning,
  }: {
    onOpen: () => void;
    isRunning: boolean;
  }) => (
    <button
      type="button"
      data-running={isRunning ? "true" : "false"}
      onClick={onOpen}
    >
      open-chat
    </button>
  ),
}));

vi.mock("./note-assistant-quick-actions", () => ({
  NoteAssistantQuickActions: ({
    onAction,
  }: {
    onAction: (action: "period_brief" | "highlights") => void;
  }) => (
    <div data-testid="note-assistant-quick-actions">
      <button type="button" onClick={() => onAction("period_brief")}>
        open-period
      </button>
      <button type="button" onClick={() => onAction("highlights")}>
        open-highlights
      </button>
    </div>
  ),
}));

vi.mock("../chat/components/chat-window", () => ({
  ChatWindow: ({
    composerAccessory,
    transcriptAccessory,
    messageAccessory,
    composerPrefix,
    threadActions,
    transformOutgoing,
    onSendOverride,
    onSendIntercept,
    onSendAccepted,
    onLockedComposerStop,
    seedSend,
    layout,
    contextNotePageId,
  }: {
    composerAccessory?: ReactNode;
    transcriptAccessory?: ReactNode;
    messageAccessory?: (message: { id: string; content?: string; parts?: { type: string }[] }) => ReactNode;
    composerPrefix?: ReactNode;
    threadActions?: ReactNode;
    transformOutgoing?: (content: string) => string;
    onSendOverride?: (text: string) => boolean | Promise<boolean>;
    onSendIntercept?: (text: string) => boolean;
    onSendAccepted?: () => void;
    onLockedComposerStop?: () => void;
    seedSend?: { nonce: number; text: string } | null;
    layout?: string;
    contextNotePageId?: string;
  }) => {
    const sessionId = contextNotePageId
      ? chatState.noteBubbleActiveSessionByPage[contextNotePageId] ?? ""
      : "";
    const { data: messages = [] } = useQuery({
      ...chatMessagesOptions(sessionId),
      enabled: Boolean(sessionId),
    });
    return (
    <div>
      <div data-testid="chat-window" data-layout={layout}>
        {composerPrefix}
        <div data-testid="transcript-slot">
          {messages.map((message: { id: string; content?: string; parts?: { type: string }[] }) => (
            <div key={message.id}>{messageAccessory?.(message)}</div>
          ))}
          {transcriptAccessory}
        </div>
        <div data-testid="thread-actions-slot">{threadActions}</div>
        <div data-testid="composer-slot">{composerAccessory}</div>
      </div>
      {seedSend ? <div data-testid="seed-send">{seedSend.text}</div> : null}
      <button
        type="button"
        onClick={() => {
          lastOutgoing.text = transformOutgoing?.("这句话想表达什么？") ?? "这句话想表达什么？";
          onSendAccepted?.();
        }}
      >
        send-quoted
      </button>
      <button
        type="button"
        onClick={() => {
          if (onSendIntercept?.("帮我写汇报")) return;
          if (onSendOverride) {
            void onSendOverride("帮我写汇报");
            return;
          }
          lastOutgoing.text = "帮我写汇报";
        }}
      >
        send-intent
      </button>
      <button type="button" onClick={() => void onSendOverride?.("只采集 Cloud Box")}>
        send-override
      </button>
      <button type="button" onClick={() => onLockedComposerStop?.()}>
        stop-locked
      </button>
    </div>
    );
  },
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

function sessionPlan(overrides: Record<string, unknown> = {}) {
  return {
    window: "week",
    date: "2026-09-03",
    start_date: "",
    end_date: "",
    collector_agent_ids: ["collector-a", "collector-b"],
    focus: "",
    ...overrides,
  };
}

describe("NoteAssistantBubble period brief", () => {
  beforeEach(() => {
    listAgents.mockReset();
    listRuntimes.mockReset();
    listMembers.mockReset();
    listChatSessions.mockReset();
    listChatMessages.mockReset();
    listPendingChatTasks.mockReset();
    ensureNotesAssistantAgent.mockReset();
    ensurePeriodBriefCollectors.mockReset();
    createNotePeriodBrief.mockReset();
    cancelNotePeriodBrief.mockReset();
    deleteNotePeriodBriefPlan.mockReset();
    getActiveNotePeriodBrief.mockReset();
    getNotePeriodBriefPlan.mockReset();
    putNotePeriodBriefPlan.mockReset();
    toggleNoteBubble.mockReset();
    setNoteBubbleOpenPageId.mockReset();
    setNoteBubbleActiveSession.mockReset();
    setNoteSelectionQuote.mockReset();
    removeNoteSelectionExcerpt.mockReset();
    chatState.noteBubbleOpenPageId = null;
    chatState.noteBubbleActiveSessionByPage = {};
    chatState.inputDrafts = {};
    chatState.noteSelectionQuote = null;
    lastOutgoing.text = "";
    getActiveNotePeriodBrief.mockResolvedValue({ run: null });
    getNotePeriodBriefPlan.mockResolvedValue({ plan: null });
    putNotePeriodBriefPlan.mockImplementation(async (data: {
      window?: string;
      date?: string;
      start_date?: string;
      end_date?: string;
      collector_agent_ids?: string[];
      focus?: string;
    }) => ({
      plan: {
        window: data.window ?? "week",
        date: data.date ?? "",
        start_date: data.start_date ?? "",
        end_date: data.end_date ?? "",
        collector_agent_ids: data.collector_agent_ids ?? [],
        focus: data.focus ?? "",
      },
    }));
    cancelNotePeriodBrief.mockResolvedValue({ run: { id: "run-1", status: "cancelled" } });
    deleteNotePeriodBriefPlan.mockResolvedValue({ plan: null });
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
    listChatMessages.mockResolvedValue([]);
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

  it("starts 写汇报 from the session plan, not a local chip draft", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
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

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(screen.getByTestId("period-brief-start"));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith({
        agent_id: "notes-1",
        timezone: "UTC",
        context_note_page_id: "page-1",
        chat_session_id: "sess-1",
        from_chat: true,
      });
    });
    expect(createNotePeriodBrief.mock.calls[0]?.[0]).not.toHaveProperty("window");
    expect(createNotePeriodBrief.mock.calls[0]?.[0]).not.toHaveProperty("collector_agent_ids");
    expect(setNoteBubbleActiveSession).toHaveBeenCalledWith("page-1", "session-brief");
    expect(screen.queryByTestId("seed-send")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps the plan card after a failed start", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
    createNotePeriodBrief.mockRejectedValue(new Error("collectors offline"));
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

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-start"));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalled();
    });
    expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    expect(screen.getByTestId("period-brief-start")).not.toBeDisabled();
    expect(screen.getByTestId("period-brief-compose")).toHaveAttribute("data-spent", "false");
  });

  it("reuses the open bubble session when starting 写汇报", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "old-qa-session" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
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

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-start")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-start"));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          chat_session_id: "old-qa-session",
          from_chat: true,
        }),
      );
    });
    expect(setNoteBubbleActiveSession).toHaveBeenCalledWith("page-1", "session-brief");
  });

  it("writes chip edits onto the session plan", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
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

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-window-day")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-window-day"));
    await waitFor(() => {
      expect(putNotePeriodBriefPlan).toHaveBeenCalledWith(
        expect.objectContaining({
          chat_session_id: "sess-1",
          window: "day",
          collector_agent_ids: ["collector-a", "collector-b"],
        }),
      );
    });
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("cancels the plan card and ends 写汇报", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
    deleteNotePeriodBriefPlan.mockImplementation(async () => {
      getNotePeriodBriefPlan.mockResolvedValue({ plan: null });
      return { plan: null };
    });
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

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-cancel")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-cancel"));
    await waitFor(() => {
      expect(deleteNotePeriodBriefPlan).toHaveBeenCalledWith("sess-1");
    });
    await waitFor(() => {
      expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    });
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("keeps Start disabled when the session plan has no computers", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({
      plan: sessionPlan({ collector_agent_ids: [] }),
    });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-start")).toBeDisabled();
    });
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("marks the FAB running while a page chat task is in flight", async () => {
    listChatSessions.mockResolvedValue([
      {
        id: "sess-qa",
        workspace_id: "ws-1",
        agent_id: "notes-1",
        creator_id: "user-1",
        title: "Note chat",
        status: "active",
        context_note_page_id: "page-1",
        has_unread: false,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    ]);
    listPendingChatTasks.mockResolvedValue({
      tasks: [{ chat_session_id: "sess-qa" }],
    });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "open-chat" })).toHaveAttribute(
        "data-running",
        "true",
      );
    });
  });

  it("stops a collecting period brief from the composer stop button", async () => {
    getActiveNotePeriodBrief.mockResolvedValue({
      run: { id: "run-1", status: "collecting", draft_page_id: "draft-1" },
    });
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
    await waitFor(() => {
      expect(getActiveNotePeriodBrief).toHaveBeenCalled();
    });
    await user.click(screen.getByRole("button", { name: "stop-locked" }));
    await waitFor(() => {
      expect(cancelNotePeriodBrief).toHaveBeenCalledWith("run-1");
    });
  });

  it("marks the FAB running while a period brief is collecting", async () => {
    getActiveNotePeriodBrief.mockResolvedValue({
      run: { id: "run-1", status: "collecting", chat_session_id: "session-brief" },
    });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "open-chat" })).toHaveAttribute(
        "data-running",
        "true",
      );
    });
  });

  it("does not lock the composer when only the insert card is waiting", async () => {
    getActiveNotePeriodBrief.mockResolvedValue({
      run: { id: "run-1", status: "awaiting_confirm", chat_session_id: "session-brief" },
    });
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    chatState.noteBubbleOpenPageId = "page-1";
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => {
      expect(getActiveNotePeriodBrief).toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: "open-chat" })).toBeNull();
      expect(screen.getByRole("button", { name: "open-period" })).toBeTruthy();
    });
    await user.click(screen.getByRole("button", { name: "open-period" }));
    await waitFor(() => {
      expect(screen.getByTestId("seed-send")).toHaveTextContent("写汇报");
    });
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
  });

  it("sends 写汇报 from the in-window action without a local plan card", async () => {
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    chatState.noteBubbleOpenPageId = "page-1";
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await user.click(screen.getByRole("button", { name: "open-period" }));
    await waitFor(() => {
      expect(screen.getByTestId("seed-send")).toHaveTextContent("写汇报");
    });
    expect(screen.getByTestId("seed-send")).not.toHaveTextContent("<period_brief_compose");
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("sends 写汇报 to the assistant without a local confirm card", async () => {
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
    expect(screen.queryByTestId("period-brief-intent-confirm")).toBeNull();
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(lastOutgoing.text).toBe("帮我写汇报");
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("shows 是/否 buttons while awaiting intent and seeds the reply", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({
      plan: sessionPlan({ status: "awaiting_intent", window: "", collector_agent_ids: [] }),
    });
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

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-intent-confirm")).toBeTruthy();
    });
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();

    await user.click(screen.getByTestId("period-brief-intent-yes"));
    await waitFor(() => {
      expect(screen.getByTestId("seed-send")).toHaveTextContent("是");
    });

    getNotePeriodBriefPlan.mockResolvedValue({
      plan: sessionPlan({ status: "awaiting_intent", window: "", collector_agent_ids: [] }),
    });
    await user.click(screen.getByTestId("period-brief-intent-no"));
    await waitFor(() => {
      expect(screen.getByTestId("seed-send")).toHaveTextContent("否");
    });
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("does not open the plan card from a historical compose fence", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    listChatMessages.mockResolvedValue([
      {
        id: "m-compose-old",
        chat_session_id: "sess-1",
        role: "assistant",
        content: "好，我打开写汇报选项。\n<period_brief_compose/>",
        task_id: null,
        created_at: "2026-01-01T00:00:00Z",
      },
    ]);
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => expect(getNotePeriodBriefPlan).toHaveBeenCalled());
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(toggleNoteBubble).not.toHaveBeenCalled();
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("opens the plan card when the session has a current plan", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    expect(
      screen.getByTestId("transcript-slot").querySelector("[data-testid='period-brief-compose']"),
    ).toBeTruthy();
    expect(
      screen.getByTestId("composer-slot").querySelector("[data-testid='period-brief-compose']"),
    ).toBeNull();
    expect(toggleNoteBubble).toHaveBeenCalledWith("page-1");
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("shows the plan card after the assistant writes a week-only session plan", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: null });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => expect(getNotePeriodBriefPlan).toHaveBeenCalled());
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();

    getNotePeriodBriefPlan.mockResolvedValue({
      plan: sessionPlan({ collector_agent_ids: [] }),
    });
    await act(async () => {
      await qc.invalidateQueries({ queryKey: noteKeys.periodBriefPlan("ws-1", "sess-1") });
    });
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    expect(screen.getByTestId("period-brief-start")).toBeDisabled();
  });

  it("hides the plan card after the session plan is consumed", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: null });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => expect(getNotePeriodBriefPlan).toHaveBeenCalled());
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("does not start from a chat start fence; the assistant start tool does", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
    chatState.inputDrafts = { "sess-1": "只整理 ~/multica" };
    listChatMessages.mockResolvedValue([]);
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    qc.setQueryData(chatKeys.messages("sess-1"), [
      {
        id: "m-start",
        chat_session_id: "sess-1",
        role: "assistant",
        content: "开始采集。\n<period_brief_start/>",
        task_id: null,
        created_at: "2026-01-01T00:00:01Z",
      },
    ]);
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
    expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
  });

  it("does not resynth from a chat resynth fence", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan() });
    listChatMessages.mockResolvedValue([]);
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    qc.setQueryData(chatKeys.messages("sess-1"), [
      {
        id: "m-resynth",
        chat_session_id: "sess-1",
        role: "assistant",
        content: "我把两台电脑的材料并在一起。\n<period_brief_resynth/>",
        task_id: null,
        created_at: "2026-01-01T00:00:01Z",
      },
    ]);
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("does not replay a historical resynth fence", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    listChatMessages.mockResolvedValue([
      {
        id: "m-resynth-old",
        chat_session_id: "sess-1",
        role: "assistant",
        content: "并入上一台电脑。\n<period_brief_resynth/>",
        task_id: null,
        created_at: "2026-01-01T00:00:00Z",
      },
    ]);
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    await waitFor(() => expect(getNotePeriodBriefPlan).toHaveBeenCalled());
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("shows a collector setup card instead of silently creating agents", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({ plan: sessionPlan({ collector_agent_ids: [] }) });
    listAgents.mockResolvedValue([agent()]);
    listRuntimes.mockResolvedValue([
      {
        id: "runtime-1",
        daemon_id: "pc-daemon-aaaa",
        status: "online",
        runtime_mode: "local",
        owner_id: "user-1",
        display_name: "Laptop A",
        name: "laptop-a",
      },
      {
        id: "runtime-cloud",
        daemon_id: "cloud-box",
        status: "online",
        runtime_mode: "cloud",
        owner_id: "user-1",
        display_name: "Cloud Box",
        name: "cloud-box",
      },
    ]);
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-missing-local:pc-daemon-aaaa")).toBeTruthy();
      expect(screen.getByTestId("period-brief-collector-missing-cloud:runtime-cloud")).toBeTruthy();
    });
    expect(screen.getByText("Laptop A")).toBeTruthy();
    expect(screen.getByText("Cloud Box")).toBeTruthy();
    expect(ensurePeriodBriefCollectors).not.toHaveBeenCalled();
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

  it("shows an abbreviated selection quote in the composer", () => {
    chatState.noteBubbleOpenPageId = "page-1";
    chatState.noteSelectionQuote = {
      pageId: "page-1",
      excerpts: [{ id: "e1", text: `${"选中内容".repeat(20)}尾部不应出现在缩略引用里` }],
      askedAt: 1,
    };
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    const chip = screen.getByTestId("note-selection-quote-preview");
    expect(chip.textContent).toContain("选中内容");
    expect(chip.textContent).toContain("…");
    expect(chip.textContent ?? "").not.toContain("尾部不应出现在缩略引用里");
  });

  it("sends the full excerpt with the question and then clears the quote", async () => {
    const user = userEvent.setup();
    chatState.noteBubbleOpenPageId = "page-1";
    chatState.noteSelectionQuote = {
      pageId: "page-1",
      excerpts: [{ id: "e1", text: "完整选区\n第二行" }],
      askedAt: 1,
    };
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    await user.click(screen.getByRole("button", { name: "send-quoted" }));
    expect(lastOutgoing.text).toBe("> 完整选区\n> 第二行\n\n这句话想表达什么？");
    expect(setNoteSelectionQuote).toHaveBeenCalledWith(null);
  });

  it("keeps multiple selection quotes in the composer and sends them together", async () => {
    const user = userEvent.setup();
    chatState.noteBubbleOpenPageId = "page-1";
    chatState.noteSelectionQuote = {
      pageId: "page-1",
      excerpts: [
        { id: "e1", text: "第一段" },
        { id: "e2", text: "第二段" },
      ],
      askedAt: 1,
    };
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
    const rows = screen.getAllByTestId("note-selection-quote-excerpt");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("第一段");
    expect(rows[1]?.textContent).toContain("第二段");
    await user.click(screen.getByRole("button", { name: "send-quoted" }));
    expect(lastOutgoing.text).toBe("> 第一段\n\n> 第二段\n\n这句话想表达什么？");
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

describe("NoteAssistantBubble highlights", () => {
  beforeEach(() => {
    listAgents.mockReset();
    listRuntimes.mockReset();
    listMembers.mockReset();
    listChatSessions.mockReset();
    listChatMessages.mockReset();
    listPendingChatTasks.mockReset();
    ensureNotesAssistantAgent.mockReset();
    createNotePeriodBrief.mockReset();
    cancelNotePeriodBrief.mockReset();
    deleteNotePeriodBriefPlan.mockReset();
    getActiveNotePeriodBrief.mockReset();
    getNotePeriodBriefPlan.mockReset();
    putNotePeriodBriefPlan.mockReset();
    toggleNoteBubble.mockReset();
    setNoteBubbleOpenPageId.mockReset();
    chatState.noteBubbleOpenPageId = null;
    chatState.noteBubbleActiveSessionByPage = {};
    chatState.inputDrafts = {};
    chatState.noteSelectionQuote = null;
    lastOutgoing.text = "";
    getActiveNotePeriodBrief.mockResolvedValue({ run: null });
    getNotePeriodBriefPlan.mockResolvedValue({ plan: null });
    listAgents.mockResolvedValue([agent()]);
    listRuntimes.mockResolvedValue([
      { id: "runtime-1", status: "online", runtime_mode: "local", owner_id: "user-1" },
    ]);
    listMembers.mockResolvedValue([]);
    listChatSessions.mockResolvedValue([]);
    listChatMessages.mockResolvedValue([]);
    listPendingChatTasks.mockResolvedValue({ tasks: [] });
    ensureNotesAssistantAgent.mockResolvedValue({
      created: false,
      needs_setup: false,
      onboarding_available: false,
      setup_hint: false,
      agent: agent(),
    });
  });

  function renderBubble() {
    chatState.noteBubbleOpenPageId = "page-1";
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );
  }

  it("opens a compose card instead of sending immediately", async () => {
    const user = userEvent.setup();
    renderBubble();

    await user.click(screen.getByRole("button", { name: "open-highlights" }));
    expect(screen.getByTestId("highlights-compose")).toBeTruthy();
    expect(screen.getByRole("textbox")).toHaveValue(
      "请整理本笔记以及它的子笔记的重点。先用 notes 工具读取当前页及其子树，再按层级列出每页的核心结论、待办和未决问题。不要复述全文，写成可读提纲。",
    );
    expect(screen.queryByTestId("seed-send")).toBeNull();
  });

  it("sends the edited prompt from the card", async () => {
    const user = userEvent.setup();
    renderBubble();

    await user.click(screen.getByRole("button", { name: "open-highlights" }));
    const editor = screen.getByRole("textbox");
    await user.clear(editor);
    await user.type(editor, "只要待办");
    await user.click(screen.getByTestId("highlights-send"));

    expect(screen.getByTestId("seed-send")).toHaveTextContent("只要待办");
    expect(screen.queryByTestId("highlights-compose")).toBeNull();
  });

  it("cancels without sending", async () => {
    const user = userEvent.setup();
    renderBubble();

    await user.click(screen.getByRole("button", { name: "open-highlights" }));
    await user.click(screen.getByTestId("highlights-cancel"));

    expect(screen.queryByTestId("highlights-compose")).toBeNull();
    expect(screen.queryByTestId("seed-send")).toBeNull();
  });

  it("closes the period brief card when opening highlights", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "sess-1" };
    getNotePeriodBriefPlan.mockResolvedValue({
      plan: {
        window: "week",
        date: "2026-09-03",
        start_date: "",
        end_date: "",
        collector_agent_ids: ["collector-a"],
        focus: "",
      },
    });
    const user = userEvent.setup();
    renderBubble();

    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });

    await user.click(screen.getByRole("button", { name: "open-highlights" }));
    expect(screen.getByTestId("highlights-compose")).toBeTruthy();
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(screen.queryByTestId("seed-send")).toBeNull();
  });

  it("closes the highlights card when opening period brief", async () => {
    const user = userEvent.setup();
    renderBubble();

    await user.click(screen.getByRole("button", { name: "open-highlights" }));
    expect(screen.getByTestId("highlights-compose")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "open-period" }));
    await waitFor(() => {
      expect(screen.getByTestId("seed-send")).toHaveTextContent("写汇报");
    });
    expect(screen.queryByTestId("highlights-compose")).toBeNull();
    expect(screen.getByTestId("seed-send")).not.toHaveTextContent("<period_brief_compose");
  });

  it("keeps an in-progress draft when highlights is clicked again", async () => {
    const user = userEvent.setup();
    renderBubble();

    await user.click(screen.getByRole("button", { name: "open-highlights" }));
    await user.clear(screen.getByRole("textbox"));
    await user.type(screen.getByRole("textbox"), "只要待办");
    await user.click(screen.getByRole("button", { name: "open-highlights" }));

    expect(screen.getByRole("textbox")).toHaveValue("只要待办");
    expect(screen.queryByTestId("seed-send")).toBeNull();
  });
});
