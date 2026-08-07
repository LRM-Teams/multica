import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enAgents from "../../../locales/en/agents.json";
import enCommon from "../../../locales/en/common.json";

const runnerActivity = vi.fn();
vi.mock("@multica/core/agents", () => ({ useRunnerActivity: (...args: unknown[]) => runnerActivity(...args) }));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("../../../common/use-viewing-timezone", () => ({ useViewingTimezone: () => "UTC" }));

import { ActivityTab } from "./activity-tab";
import { copyText } from "@multica/ui/lib/clipboard";

const agent = { id: "agent-1", workspace_id: "workspace-1" } as never;
const TEST_RESOURCES = { en: { agents: enAgents, common: enCommon } };

function renderActivityTab() {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ActivityTab agent={agent} />
    </I18nProvider>,
  );
}

describe("ActivityTab", () => {
  it("renders the server-projected rows in the previous chronological timeline UI", () => {
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Running command...", tone: "active", visibility: "visible" },
        timeline: [
          { id: "row-2", occurred_at: "2026-08-06T12:01:00Z", title: "Idle", tone: "success", body_kind: "none" },
          { id: "row-1", occurred_at: "2026-08-06T12:00:00Z", title: "Running command...", subtext: "Safe detail", tone: "active", body_kind: "text", body: "sanitized body" },
        ],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    renderActivityTab();
    expect(runnerActivity).toHaveBeenCalledWith("workspace-1", "agent-1");
    expect(screen.getByTestId("activity-timeline-spine")).toBeInTheDocument();
    expect(screen.getAllByTestId("runner-activity-row").map((row) => row.textContent)).toEqual([
      expect.stringContaining("Running command..."),
      expect.stringContaining("Idle"),
    ]);
    expect(screen.getAllByText("Running command...")).toHaveLength(1);
    expect(screen.getByText("Safe detail")).toHaveClass("block", "break-words");
    fireEvent.click(screen.getByText("Running command..."));
    expect(screen.getByText("sanitized body")).toBeInTheDocument();
  });

  it("does not invent a summary when the server withholds it", () => {
    runnerActivity.mockReturnValue({ data: { summary: null, timeline: [] }, isLoading: false, isError: false, refetch: vi.fn() });
    renderActivityTab();
    expect(screen.getByText("No activity yet")).toBeInTheDocument();
  });

  it("safely renders unknown server presentation and supports expand and copy", async () => {
    vi.mocked(copyText).mockResolvedValue(true);
    const body = "safe detail ".repeat(30);
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Future status", tone: "future-tone", visibility: "visible" },
        timeline: [{ id: "row-2", occurred_at: "not-a-date", title: "Future event", tone: "future-tone", body_kind: "future-body", body }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });

    renderActivityTab();
    expect(screen.queryByText("Future status")).not.toBeInTheDocument();
    expect(screen.getByText("not-a-date")).toBeInTheDocument();
    expect(screen.getByText("Future event")).toBeInTheDocument();
    const rowToggle = screen.getByRole("button", { name: /Future event/ });
    expect(rowToggle).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(rowToggle);
    expect(rowToggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(screen.getByText("Copy"));
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(body.trim()));
    expect(screen.getByText("Copied")).toBeInTheDocument();
  });
});
