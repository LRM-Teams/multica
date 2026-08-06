import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const runnerActivity = vi.fn();
vi.mock("@multica/core/agents", () => ({ useRunnerActivity: (...args: unknown[]) => runnerActivity(...args) }));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: vi.fn() }));

import { ActivityTab } from "./activity-tab";
import { copyText } from "@multica/ui/lib/clipboard";

const agent = { id: "agent-1", workspace_id: "workspace-1" } as never;

describe("ActivityTab", () => {
  it("renders only the server-projected summary and timeline", () => {
    runnerActivity.mockReturnValue({
      data: {
        summary: { label: "Running command...", tone: "active", visibility: "visible" },
        timeline: [{ id: "row-1", occurred_at: "2026-08-06T12:00:00Z", title: "Running command...", subtext: "Safe detail", tone: "active", body_kind: "text", body: "sanitized body" }],
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    render(<ActivityTab agent={agent} />);
    expect(runnerActivity).toHaveBeenCalledWith("workspace-1", "agent-1");
    expect(screen.getAllByText("Running command...")).toHaveLength(2);
    expect(screen.getByText("sanitized body")).toBeInTheDocument();
  });

  it("does not invent a summary when the server withholds it", () => {
    runnerActivity.mockReturnValue({ data: { summary: null, timeline: [] }, isLoading: false, isError: false, refetch: vi.fn() });
    render(<ActivityTab agent={agent} />);
    expect(screen.getByText("No activity yet.")).toBeInTheDocument();
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

    render(<ActivityTab agent={agent} />);
    expect(screen.getByText("Future status")).toBeInTheDocument();
    expect(screen.getByText("not-a-date")).toBeInTheDocument();
    expect(screen.getByText("Future event")).toBeInTheDocument();
    expect(screen.getByText("Expand")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Expand"));
    expect(screen.getByText("Collapse")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Copy"));
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(body.trim()));
    expect(screen.getByText("Copied")).toBeInTheDocument();
  });
});
