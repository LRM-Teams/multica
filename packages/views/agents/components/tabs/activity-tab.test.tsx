import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../../locales/en/agents.json";
import enCommon from "../../../locales/en/common.json";

const runnerActivity = vi.fn();
vi.mock("@multica/core/agents", () => ({ useRunnerActivity: (...args: unknown[]) => runnerActivity(...args) }));
vi.mock("../../../common/use-viewing-timezone", () => ({ useViewingTimezone: () => "UTC" }));
import { ActivityTab } from "./activity-tab";

const agent = { id: "agent-1", workspace_id: "workspace-1" } as never;
function renderTab() {
  return render(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="en" resources={{ en: { agents: enAgents, common: enCommon } }}><ActivityTab agent={agent} /></I18nProvider></QueryClientProvider>);
}

describe("ActivityTab", () => {
  beforeEach(() => vi.clearAllMocks());

  it("renders the chronological timeline, fact color, and command details", () => {
    runnerActivity.mockReturnValue({ data: { summary: null, timeline: [
      { id: "new", occurred_at: "2026-08-06T12:01:00Z", title: "Idle", activity_kind: "online", detail_kind: "idle", body_kind: "none" },
      { id: "old", occurred_at: "2026-08-06T12:00:00Z", title: "Custom command title", activity_kind: "working", detail_kind: "running_command", body_kind: "command", body: "git status" },
    ] }, isLoading: false, isError: false, refetch: vi.fn() });
    renderTab();
    expect(runnerActivity).toHaveBeenCalledWith("workspace-1", "agent-1");
    expect(screen.getAllByTestId("runner-activity-row").map((row) => row.textContent)).toEqual([expect.stringContaining("Custom command title"), expect.stringContaining("Idle")]);
    expect(screen.getAllByTestId("runner-activity-row")[0]?.querySelector(".bg-running")).not.toBeNull();
    expect(screen.getByTestId("activity-command-block")).toHaveTextContent("git status");
  });

  it("renders loading, error/retry, and empty states", () => {
    runnerActivity.mockReturnValue({ isLoading: true, isError: false, refetch: vi.fn() });
    const view = renderTab();
    expect(screen.getByTestId("activity-timeline-loading")).toBeInTheDocument();
    const refetch = vi.fn();
    runnerActivity.mockReturnValue({ isLoading: false, isError: true, refetch });
    view.rerender(<QueryClientProvider client={new QueryClient()}><I18nProvider locale="en" resources={{ en: { agents: enAgents, common: enCommon } }}><ActivityTab agent={agent} /></I18nProvider></QueryClientProvider>);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(refetch).toHaveBeenCalled();
    view.unmount();
    runnerActivity.mockReturnValue({ data: { summary: null, timeline: [] }, isLoading: false, isError: false, refetch: vi.fn() });
    renderTab();
    expect(screen.getByTestId("activity-timeline-empty")).toHaveTextContent("No activity yet");
  });
});
