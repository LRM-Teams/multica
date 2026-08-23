/**
 * @vitest-environment happy-dom
 */
import type { ComponentProps } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, fireEvent, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NotePeriodBriefCompose } from "./note-period-brief-compose";

const { listAgents, listRuntimes, ensurePeriodBriefCollectors } = vi.hoisted(() => ({
  listAgents: vi.fn(),
  listRuntimes: vi.fn(),
  ensurePeriodBriefCollectors: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listAgents: (...args: unknown[]) => listAgents(...args),
    listRuntimes: (...args: unknown[]) => listRuntimes(...args),
    ensurePeriodBriefCollectors: (...args: unknown[]) => ensurePeriodBriefCollectors(...args),
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

function renderCompose(
  props: Partial<ComponentProps<typeof NotePeriodBriefCompose>> = {},
  locale: "en" | "zh-Hans" = "zh-Hans",
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onResolvedChange = vi.fn();
  const result = renderWithI18n(
    <QueryClientProvider client={qc}>
      <NotePeriodBriefCompose active onResolvedChange={onResolvedChange} {...props} />
    </QueryClientProvider>,
    { locale },
  );
  return { ...result, onResolvedChange };
}

describe("NotePeriodBriefCompose", () => {
  beforeEach(() => {
    listAgents.mockReset();
    listRuntimes.mockReset();
    ensurePeriodBriefCollectors.mockReset();
    const collectorA = agent({
      id: "collector-a",
      name: "period-collect-laptopa",
      display_name: "采集 · Laptop A",
      runtime_id: "runtime-1",
      runtime_mode: "local",
      runtime_status: "online",
      owner_id: "user-1",
    });
    const collectorB = agent({
      id: "collector-b",
      name: "period-collect-cloud01",
      display_name: "采集 · 云端 · Cloud Box",
      runtime_id: "runtime-cloud",
      runtime_mode: "cloud",
      runtime_status: "online",
      owner_id: "user-1",
    });
    const foreignCollector = agent({
      id: "collector-foreign",
      name: "period-collect-foreign",
      display_name: "采集 · Other Laptop",
      runtime_id: "runtime-foreign",
      runtime_mode: "local",
      runtime_status: "online",
      owner_id: "user-2",
    });
    listAgents.mockResolvedValue([
      agent(),
      collectorA,
      collectorB,
      foreignCollector,
      agent({
        id: "notes-1",
        name: "notes-assistant",
        display_name: "笔记助手",
        runtime_status: "online",
      }),
    ]);
    listRuntimes.mockResolvedValue([
      { id: "runtime-1", status: "online", runtime_mode: "local", owner_id: "user-1" },
      { id: "runtime-cloud", status: "online", runtime_mode: "cloud", owner_id: "user-1" },
      { id: "runtime-foreign", status: "online", runtime_mode: "local", owner_id: "user-2" },
      {
        id: "runtime-c",
        daemon_id: "pc-daemon-cccc",
        status: "online",
        runtime_mode: "local",
        owner_id: "user-1",
        display_name: "Laptop C",
        name: "laptop-c",
      },
    ]);
    ensurePeriodBriefCollectors.mockResolvedValue({
      agents: [collectorA, collectorB],
      created: [],
    });
  });

  it("shows window and owned-computer chips, not a dedicated dialog", async () => {
    renderCompose();
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-a")).toBeTruthy();
    });
    expect(screen.getByTestId("period-brief-window-day")).toBeTruthy();
    expect(screen.getByTestId("period-brief-window-week")).toBeTruthy();
    expect(screen.getByTestId("period-brief-window-month")).toBeTruthy();
    expect(screen.getByTestId("period-brief-window-custom")).toBeTruthy();
    expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    expect(screen.queryByTestId("period-brief-collector-collector-foreign")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByTestId("period-brief-focus")).toBeNull();
    expect(ensurePeriodBriefCollectors).not.toHaveBeenCalled();
  });

  it("reports chip selections and lets the user toggle computers", async () => {
    const user = userEvent.setup();
    const { onResolvedChange } = renderCompose();
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    });
    await waitFor(() => {
      expect(onResolvedChange).toHaveBeenCalledWith(
        expect.objectContaining({
          agentId: "notes-1",
          request: expect.objectContaining({
            window: "week",
            collector_ids: expect.arrayContaining(["collector-a", "collector-b"]),
          }),
        }),
      );
    });
    await user.click(screen.getByTestId("period-brief-collector-collector-a"));
    await waitFor(() => {
      expect(onResolvedChange).toHaveBeenCalledWith(
        expect.objectContaining({
          request: expect.objectContaining({
            collector_ids: ["collector-b"],
          }),
        }),
      );
    });
  });

  it("collects an inclusive custom range from the chips", async () => {
    const user = userEvent.setup();
    const { onResolvedChange } = renderCompose();
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
    await waitFor(() => {
      expect(onResolvedChange).toHaveBeenCalledWith(
        expect.objectContaining({
          request: expect.objectContaining({
            window: "custom",
            start_date: "2026-08-10",
            end_date: "2026-08-14",
          }),
        }),
      );
    });
  });

  it("lets typed text override conflicting chip selections", async () => {
    const { onResolvedChange } = renderCompose({ text: "只采集 Cloud Box 的本月" });
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-collector-b")).toBeTruthy();
    });
    await waitFor(() => {
      expect(onResolvedChange).toHaveBeenCalledWith(
        expect.objectContaining({
          request: expect.objectContaining({
            window: "month",
            collector_ids: ["collector-b"],
            focus: "只采集 Cloud Box 的本月",
          }),
        }),
      );
    });
  });

  it("reminds about a computer with no collector without blocking the rest", async () => {
    const user = userEvent.setup();
    const onConfigureCollector = vi.fn();
    const { onResolvedChange } = renderCompose({ onConfigureCollector });
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-collector-missing-local:pc-daemon-cccc")).toBeTruthy();
    });
    expect(screen.getByText("Laptop C")).toBeTruthy();
    expect(screen.getByTestId("period-brief-collector-collector-a")).toBeTruthy();
    await waitFor(() => {
      expect(onResolvedChange).toHaveBeenCalledWith(
        expect.objectContaining({
          canSubmit: true,
          request: expect.objectContaining({
            collector_ids: expect.arrayContaining(["collector-a", "collector-b"]),
          }),
        }),
      );
    });
    const missing = screen.getByTestId("period-brief-collector-missing-local:pc-daemon-cccc");
    await user.click(within(missing).getByTestId("period-brief-collector-missing-configure"));
    expect(onConfigureCollector).toHaveBeenCalledWith(
      expect.objectContaining({ label: "Laptop C", needsSetup: true }),
    );
    await user.click(within(missing).getByTestId("period-brief-collector-missing-dismiss"));
    expect(screen.queryByTestId("period-brief-collector-missing-local:pc-daemon-cccc")).toBeNull();
    expect(screen.getByTestId("period-brief-collector-collector-a")).toBeTruthy();
  });

  it("offers cancel while choosing chips", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    renderCompose({ onCancel });
    const cancel = screen.getByTestId("period-brief-cancel");
    expect(cancel).toBeEnabled();
    expect(cancel).toHaveTextContent("取消");
    await user.click(cancel);
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("disables cancel once submit starts", () => {
    renderCompose({ onCancel: vi.fn(), submitting: true });
    expect(screen.getByTestId("period-brief-cancel")).toBeDisabled();
  });

  it("shows the in-bubble run status instead of jumping away", () => {
    renderCompose({
      startedTitle: "工作介绍 本周 · 底稿",
    });
    expect(screen.getByTestId("period-brief-started")).toBeTruthy();
    expect(screen.getByText(/工作介绍 本周/)).toBeTruthy();
  });
});
