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
  setNoteSelectionQuote,
  removeNoteSelectionExcerpt,
  lastOutgoing,
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
  setNoteSelectionQuote: vi.fn(),
  removeNoteSelectionExcerpt: vi.fn(),
  lastOutgoing: { text: "" },
}));

const chatState = {
  noteBubbleOpenPageId: null as string | null,
  noteBubbleActiveSessionByPage: {} as Record<string, string>,
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
    listPendingChatTasks: (...args: unknown[]) => listPendingChatTasks(...args),
    ensureNotesAssistantAgent: (...args: unknown[]) => ensureNotesAssistantAgent(...args),
    ensurePeriodBriefCollectors: (...args: unknown[]) => ensurePeriodBriefCollectors(...args),
    createNotePeriodBrief: (...args: unknown[]) => createNotePeriodBrief(...args),
    getActiveNotePeriodBrief: (...args: unknown[]) => getActiveNotePeriodBrief(...args),
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

vi.mock("./note-assistant-fab-cluster", () => ({
  NoteAssistantFabCluster: ({
    onAction,
    isRunning,
  }: {
    onAction: (action: "period_brief" | "highlights" | "chat") => void;
    isRunning: boolean;
  }) => (
    <>
      <button
        type="button"
        data-running={isRunning ? "true" : "false"}
        onClick={() => onAction("period_brief")}
      >
        open-period
      </button>
      <button type="button" onClick={() => onAction("highlights")}>
        open-highlights
      </button>
    </>
  ),
}));

vi.mock("../chat/components/chat-window", () => ({
  ChatWindow: ({
    composerAccessory,
    composerPrefix,
    transformOutgoing,
    onSendOverride,
    onSendIntercept,
    onSendAccepted,
    seedSend,
    layout,
  }: {
    composerAccessory?: ReactNode;
    composerPrefix?: ReactNode;
    transformOutgoing?: (content: string) => string;
    onSendOverride?: (text: string) => boolean | Promise<boolean>;
    onSendIntercept?: (text: string) => boolean;
    onSendAccepted?: () => void;
    seedSend?: { nonce: number; text: string } | null;
    layout?: string;
  }) => (
    <div>
      <div data-testid="chat-window" data-layout={layout}>
        {composerPrefix}
        {composerAccessory}
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
    setNoteSelectionQuote.mockReset();
    removeNoteSelectionExcerpt.mockReset();
    chatState.noteBubbleOpenPageId = null;
    chatState.noteBubbleActiveSessionByPage = {};
    chatState.noteSelectionQuote = null;
    lastOutgoing.text = "";
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
    expect(createNotePeriodBrief.mock.calls[0]?.[0]).not.toHaveProperty("chat_session_id");
    expect(setNoteBubbleActiveSession).toHaveBeenCalledWith("page-1", "session-brief");
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(screen.queryByTestId("period-brief-cancel")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("starts 写汇报 in a new thread even when a Q&A session is already open", async () => {
    chatState.noteBubbleActiveSessionByPage = { "page-1": "old-qa-session" };
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
      expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    });
    await user.click(screen.getByRole("button", { name: "send-override" }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalled();
    });
    expect(createNotePeriodBrief.mock.calls[0]?.[0]).not.toHaveProperty("chat_session_id");
    expect(setNoteBubbleActiveSession).toHaveBeenCalledWith("page-1", "session-brief");
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
      expect(screen.getByRole("button", { name: "open-period" })).toHaveAttribute(
        "data-running",
        "true",
      );
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
      expect(screen.getByRole("button", { name: "open-period" })).toHaveAttribute(
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
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteAssistantBubble pageId="page-1" pageTitle="Note" />
      </QueryClientProvider>,
      { locale: "zh-Hans" },
    );

    await waitFor(() => {
      expect(getActiveNotePeriodBrief).toHaveBeenCalled();
      expect(screen.getByRole("button", { name: "open-period" })).toHaveAttribute(
        "data-running",
        "false",
      );
    });
    await user.click(screen.getByRole("button", { name: "open-period" }));
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
  });

  it("cancels the compose chips without starting a run", async () => {
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
      expect(screen.getByTestId("period-brief-cancel")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-cancel"));
    expect(screen.queryByTestId("period-brief-compose")).toBeNull();
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
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

  it("shows a collector setup card instead of silently creating agents", async () => {
    const user = userEvent.setup();
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

    await user.click(screen.getByRole("button", { name: "open-period" }));
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
    listPendingChatTasks.mockReset();
    ensureNotesAssistantAgent.mockReset();
    createNotePeriodBrief.mockReset();
    getActiveNotePeriodBrief.mockReset();
    toggleNoteBubble.mockReset();
    setNoteBubbleOpenPageId.mockReset();
    chatState.noteBubbleOpenPageId = null;
    chatState.noteBubbleActiveSessionByPage = {};
    chatState.noteSelectionQuote = null;
    lastOutgoing.text = "";
    getActiveNotePeriodBrief.mockResolvedValue({ run: null });
    listAgents.mockResolvedValue([agent()]);
    listRuntimes.mockResolvedValue([
      { id: "runtime-1", status: "online", runtime_mode: "local", owner_id: "user-1" },
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
  });

  function renderBubble() {
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
    expect(toggleNoteBubble).toHaveBeenCalledWith("page-1");
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
    const user = userEvent.setup();
    renderBubble();

    await user.click(screen.getByRole("button", { name: "open-period" }));
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
      expect(screen.getByTestId("period-brief-compose")).toBeTruthy();
    });
    expect(screen.queryByTestId("highlights-compose")).toBeNull();
    expect(screen.queryByTestId("seed-send")).toBeNull();
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
