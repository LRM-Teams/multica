/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, ComputerConnection } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NotePeriodBriefDialog } from "./note-period-brief-dialog";

const { listComputers, listAgents, listRuntimes, createNotePeriodBrief, createNoteRetrospective, openNoteWorkerChat, ensurePeriodBriefAgent } =
  vi.hoisted(() => ({
    listComputers: vi.fn(),
    listAgents: vi.fn(),
    listRuntimes: vi.fn(),
    createNotePeriodBrief: vi.fn(),
    createNoteRetrospective: vi.fn(),
    openNoteWorkerChat: vi.fn(),
    ensurePeriodBriefAgent: vi.fn(),
  }));

vi.mock("@multica/core/api", () => ({
  api: {
    listComputers: (...args: unknown[]) => listComputers(...args),
    listAgents: (...args: unknown[]) => listAgents(...args),
    listRuntimes: (...args: unknown[]) => listRuntimes(...args),
    createNotePeriodBrief: (...args: unknown[]) => createNotePeriodBrief(...args),
    createNoteRetrospective: (...args: unknown[]) => createNoteRetrospective(...args),
    ensurePeriodBriefAgent: (...args: unknown[]) => ensurePeriodBriefAgent(...args),
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

vi.mock("./use-open-note-worker-chat", () => ({
  useOpenNoteWorkerChat: () => ({ openNoteWorkerChat }),
}));

function computer(overrides: Partial<ComputerConnection> = {}): ComputerConnection {
  return {
    daemon_id: "computer-1",
    owner_id: "user-1",
    connected: true,
    last_seen_at: "2026-08-17T00:00:00Z",
    work_journal_enabled: false,
    ...overrides,
  };
}

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "runtime-1",
    name: "Brief Agent",
    display_name: "Brief Agent",
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
    ...overrides,
  };
}

function renderDialog(locale: "en" | "zh-Hans" = "en") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onOpenChange = vi.fn();
  const onCreated = vi.fn();
  const result = renderWithI18n(
    <QueryClientProvider client={qc}>
      <NotePeriodBriefDialog
        open
        onOpenChange={onOpenChange}
        preferredAgentId="agent-1"
        onCreated={onCreated}
      />
    </QueryClientProvider>,
    { locale },
  );
  return { ...result, onOpenChange, onCreated };
}

describe("NotePeriodBriefDialog", () => {
  beforeEach(() => {
    listComputers.mockReset();
    listAgents.mockReset();
    listRuntimes.mockReset();
    createNotePeriodBrief.mockReset();
    createNoteRetrospective.mockReset();
    openNoteWorkerChat.mockReset();
    ensurePeriodBriefAgent.mockReset();
    listComputers.mockResolvedValue([computer()]);
    listAgents.mockResolvedValue([agent()]);
    listRuntimes.mockResolvedValue([]);
    ensurePeriodBriefAgent.mockResolvedValue({
      agent: agent({ id: "weekly-1", name: "weekly-report", display_name: "周报", model: "m1" }),
      created: true,
    });
    createNotePeriodBrief.mockResolvedValue({
      page: { id: "page-1", title: "工作介绍 本周 · 底稿" },
      job: { id: "job-1", agent_id: "agent-1", channel_id: "dm-1" },
      window: { kind: "week", timezone: "UTC", start: "", end: "", label: "本周" },
      sources_used: ["issue_activity"],
      sources_empty: ["machine_work_journal"],
      sources_skipped: [],
      fact_count: 3,
    });
  });

  it("shows the uncollected hint without blocking submit", async () => {
    renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("local-machine-work-uncollected").textContent).toBe("本机工作未采集");
    });
    expect(screen.getByRole("button", { name: /开始介绍/ })).not.toBeDisabled();
  });

  it("calls createNotePeriodBrief (not retrospective) and opens Worker chat", async () => {
    const user = userEvent.setup();
    const { onCreated } = renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByText("Brief Agent")).toBeTruthy();
    });
    await user.click(screen.getByRole("button", { name: /开始介绍/ }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          window: "week",
          agent_id: "agent-1",
        }),
      );
    });
    expect(createNoteRetrospective).not.toHaveBeenCalled();
    expect(openNoteWorkerChat).toHaveBeenCalledWith(
      expect.objectContaining({ id: "job-1", agent_id: "agent-1" }),
    );
    expect(onCreated).toHaveBeenCalled();
  });

  it("prefers the weekly-report agent as default synthesizer", async () => {
    listAgents.mockResolvedValue([
      agent({ id: "agent-1", name: "wendy", display_name: "Wendy" }),
      agent({ id: "weekly-1", name: "weekly-report", display_name: "周报" }),
    ]);
    renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-default-agent")).toBeTruthy();
    });
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /开始介绍/ }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({ agent_id: "weekly-1" }),
      );
    });
  });

  it("hides the hint when Journal is on", async () => {
    listComputers.mockResolvedValue([computer({ work_journal_enabled: true })]);
    renderDialog();
    await waitFor(() => {
      expect(listComputers).toHaveBeenCalled();
    });
    expect(screen.queryByTestId("local-machine-work-uncollected")).toBeNull();
  });
});
