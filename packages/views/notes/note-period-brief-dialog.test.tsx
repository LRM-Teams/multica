/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NotePeriodBriefDialog } from "./note-period-brief-dialog";

const {
  listAgents,
  listRuntimes,
  createNotePeriodBrief,
  createNoteRetrospective,
  openNoteWorkerChat,
  ensurePeriodBriefAgent,
  ensurePeriodBriefCollectors,
} = vi.hoisted(() => ({
  listAgents: vi.fn(),
  listRuntimes: vi.fn(),
  createNotePeriodBrief: vi.fn(),
  createNoteRetrospective: vi.fn(),
  openNoteWorkerChat: vi.fn(),
  ensurePeriodBriefAgent: vi.fn(),
  ensurePeriodBriefCollectors: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: (...args: unknown[]) => listAgents(...args),
    listRuntimes: (...args: unknown[]) => listRuntimes(...args),
    createNotePeriodBrief: (...args: unknown[]) => createNotePeriodBrief(...args),
    createNoteRetrospective: (...args: unknown[]) => createNoteRetrospective(...args),
    ensurePeriodBriefAgent: (...args: unknown[]) => ensurePeriodBriefAgent(...args),
    ensurePeriodBriefCollectors: (...args: unknown[]) => ensurePeriodBriefCollectors(...args),
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

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "runtime-1",
    name: "coder",
    display_name: "Coder",
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
    listAgents.mockReset();
    listRuntimes.mockReset();
    createNotePeriodBrief.mockReset();
    createNoteRetrospective.mockReset();
    openNoteWorkerChat.mockReset();
    ensurePeriodBriefAgent.mockReset();
    ensurePeriodBriefCollectors.mockReset();
    const collectorA = agent({
      id: "collector-a",
      name: "period-collect-laptopa",
      display_name: "采集 · Laptop A",
      runtime_id: "runtime-1",
      runtime_mode: "local",
      runtime_status: "online",
    });
    const collectorB = agent({
      id: "collector-b",
      name: "period-collect-cloud01",
      display_name: "采集 · 云端 · Cloud Box",
      runtime_id: "runtime-cloud",
      runtime_mode: "cloud",
      runtime_status: "online",
    });
    listAgents.mockResolvedValue([
      agent(),
      collectorA,
      collectorB,
      agent({
        id: "weekly-1",
        name: "weekly-report",
        display_name: "周报",
        runtime_status: "online",
      }),
    ]);
    listRuntimes.mockResolvedValue([
      { id: "runtime-1", status: "online", runtime_mode: "local" },
      { id: "runtime-cloud", status: "online", runtime_mode: "cloud" },
    ]);
    ensurePeriodBriefAgent.mockResolvedValue({
      agent: agent({ id: "weekly-1", name: "weekly-report", display_name: "周报", model: "m1" }),
      created: false,
    });
    ensurePeriodBriefCollectors.mockResolvedValue({
      agents: [collectorA, collectorB],
      created: [],
    });
    createNotePeriodBrief.mockResolvedValue({
      page: { id: "page-1", title: "工作介绍 本周 · 底稿" },
      job: { id: "job-collector-1", agent_id: "collector-a", channel_id: "dm-c1" },
      window: { kind: "week", timezone: "UTC", start: "", end: "", label: "本周" },
      sources_used: ["issue_activity"],
      sources_empty: [],
      sources_skipped: [],
      fact_count: 3,
      collector_agent_ids: ["collector-a", "collector-b"],
      collector_jobs: [
        { id: "job-collector-1", agent_id: "collector-a", channel_id: "dm-c1" },
        { id: "job-collector-2", agent_id: "collector-b", channel_id: "dm-c2" },
      ],
    });
  });

  it("defaults synthesizer to 周报 and collectors to dedicated online collectors", async () => {
    const user = userEvent.setup();
    renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-default-agent")).toBeTruthy();
      expect(screen.getByTestId("period-brief-collector-collector-a")).toBeTruthy();
    });
    expect(screen.queryByTestId("period-brief-collector-agent-1")).toBeNull();
    await user.click(screen.getByRole("button", { name: /开始介绍/ }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          window: "week",
          agent_id: "weekly-1",
          collector_agent_ids: expect.arrayContaining(["collector-a", "collector-b"]),
        }),
      );
    });
    const payload = createNotePeriodBrief.mock.calls[0]?.[0] as {
      collector_agent_ids: string[];
    };
    expect(payload.collector_agent_ids).not.toContain("weekly-1");
    expect(payload.collector_agent_ids).not.toContain("agent-1");
    expect(createNoteRetrospective).not.toHaveBeenCalled();
    expect(ensurePeriodBriefCollectors).toHaveBeenCalled();
  });

  it("lets the user toggle dedicated collectors", async () => {
    const user = userEvent.setup();
    const { onCreated } = renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-collector-collector-a"));
    await user.click(screen.getByRole("button", { name: /开始介绍/ }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          agent_id: "weekly-1",
          collector_agent_ids: ["collector-b"],
        }),
      );
    });
    expect(openNoteWorkerChat).toHaveBeenCalledWith(
      expect.objectContaining({ id: "job-collector-1", agent_id: "collector-a" }),
    );
    expect(onCreated).toHaveBeenCalled();
  });

  it("blocks submit when no collector is selected", async () => {
    const user = userEvent.setup();
    renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-a")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-collector-collector-a"));
    await user.click(screen.getByTestId("period-brief-collector-collector-b"));
    expect(screen.getByRole("button", { name: /开始介绍/ })).toBeDisabled();
    expect(createNotePeriodBrief).not.toHaveBeenCalled();
  });

  it("submits an inclusive custom date range", async () => {
    const user = userEvent.setup();
    renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-window-custom")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-window-custom"));
    fireEvent.change(screen.getByTestId("period-brief-start-date"), {
      target: { value: "2026-08-10" },
    });
    fireEvent.change(screen.getByTestId("period-brief-end-date"), {
      target: { value: "2026-08-14" },
    });
    await user.click(screen.getByRole("button", { name: /开始介绍/ }));
    await waitFor(() => {
      expect(createNotePeriodBrief).toHaveBeenCalledWith(
        expect.objectContaining({
          window: "custom",
          start_date: "2026-08-10",
          end_date: "2026-08-14",
          agent_id: "weekly-1",
        }),
      );
    });
    const payload = createNotePeriodBrief.mock.calls.at(-1)?.[0] as {
      date?: string;
    };
    expect(payload.date).toBeUndefined();
  });
});
