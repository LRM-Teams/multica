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
          {
            id: "row-1",
            occurred_at: "2026-08-06T12:00:00Z",
            title: "Running command",
            tone: "warning",
            body_kind: "command",
            body: "sanitized body",
          },
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
      expect.stringContaining("Running command"),
      expect.stringContaining("Idle"),
    ]);
    expect(screen.getAllByText("Running command")).toHaveLength(1);
    // Default-full: body visible without expand (2026-08-11).
    expect(screen.getByText("sanitized body")).toBeInTheDocument();
    expect(screen.getByTestId("activity-command-block")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-command-fold-toggle")).toBeNull();
  });

  it("renders a long Running command body fully (no line-clamp) with Copy", async () => {
    vi.mocked(copyText).mockResolvedValue(true);
    const command =
      '/bin/bash -lc "pwd && rg --files -g \'!*.git\' -g \'!node_modules\' | head -80 && git status --short"';
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Running command...", tone: "warning", visibility: "visible" },
        timeline: [{
          id: "row-cmd",
          occurred_at: "2026-08-06T12:00:00Z",
          title: "Running command",
          tone: "warning",
          body_kind: "command",
          body: command,
        }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    renderActivityTab();
    const code = screen.getByTestId("activity-command-block").querySelector("code");
    expect(code?.textContent).toBe(command);
    expect(code?.textContent).not.toContain("…");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(command));
  });

  it("falls back to subtext for legacy Running command rows without body", () => {
    const command = "go test ./internal/handler -count=1";
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Running command...", tone: "warning", visibility: "visible" },
        timeline: [{
          id: "row-legacy",
          occurred_at: "2026-08-06T12:00:00Z",
          title: "Running command",
          subtext: command,
          tone: "warning",
          body_kind: "none",
        }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    renderActivityTab();
    expect(screen.getByTestId("activity-command-block").querySelector("code")?.textContent).toBe(
      command,
    );
  });

  it("renders a server-projected soft-hold row (warning tone) with its reason subtext", () => {
    // LRM soft-hold: when an agent send is freshness-held the backend projects
    // a held row into Runner Activity. The frontend is presentation-safe: it
    // must display the supplied tone/title/subtext as-is and never infer
    // runtime state from the send response.
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Message held", tone: "warning", visibility: "visible" },
        timeline: [{
          id: "row-held",
          occurred_at: "2026-08-08T04:00:00Z",
          title: "Message held — review newer messages before sending",
          subtext: "3 newer messages available — review then resend",
          tone: "warning",
          body_kind: "none",
        }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    renderActivityTab();
    expect(screen.getByText("Message held — review newer messages before sending")).toBeInTheDocument();
    expect(screen.getByText("3 newer messages available — review then resend")).toHaveClass(
      "block",
      "break-words",
    );
  });

  it("does not invent a summary when the server withholds it", () => {
    runnerActivity.mockReturnValue({ data: { summary: null, timeline: [] }, isLoading: false, isError: false, refetch: vi.fn() });
    renderActivityTab();
    expect(screen.getByText("No activity yet")).toBeInTheDocument();
  });

  it("shows body by default with always-visible Copy (no expand required)", async () => {
    vi.mocked(copyText).mockResolvedValue(true);
    const body = "safe detail ".repeat(30).trim();
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Future status", tone: "future-tone", visibility: "visible" },
        timeline: [{
          id: "row-2",
          occurred_at: "not-a-date",
          title: "Future event",
          tone: "future-tone",
          body_kind: "future-body",
          body,
        }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });

    renderActivityTab();
    expect(screen.queryByText("Future status")).not.toBeInTheDocument();
    expect(screen.getByText("not-a-date")).toBeInTheDocument();
    expect(screen.getByText("Future event")).toBeInTheDocument();
    // Full body on first paint — no expand chrome for normal length.
    expect(screen.getByTestId("activity-command-block").querySelector("code")?.textContent).toBe(body);
    expect(screen.queryByRole("button", { name: /Future event/ })).toBeNull();
    expect(screen.queryByTestId("activity-command-fold-toggle")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(body));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("soft-folds a long body and copies the full text on Copy", async () => {
    vi.mocked(copyText).mockResolvedValue(true);
    const lines = Array.from({ length: 20 }, (_, i) => `echo line-${i + 1}`);
    const full = lines.join("\n");
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Running command...", tone: "active", visibility: "visible" },
        timeline: [{
          id: "row-long",
          occurred_at: "2026-08-06T12:00:00Z",
          title: "Running command...",
          tone: "active",
          body_kind: "text",
          body: full,
        }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });

    renderActivityTab();
    const row = screen.getByTestId("runner-activity-row");
    expect(row).toHaveAttribute("data-command-long", "true");
    const code = screen.getByTestId("activity-command-block").querySelector("code");
    expect(code?.textContent).toBe(lines.slice(0, 8).join("\n"));
    expect(code?.textContent).not.toContain("echo line-20");

    const toggle = screen.getByTestId("activity-command-fold-toggle");
    expect(toggle).toHaveTextContent("Show full command");
    fireEvent.click(toggle);
    expect(toggle).toHaveTextContent("Show less");
    expect(
      screen.getByTestId("activity-command-block").querySelector("code")?.textContent,
    ).toBe(full);

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(full));
  });
});
