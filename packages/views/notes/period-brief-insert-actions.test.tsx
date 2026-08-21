/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { MessagePart } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { PeriodBriefInsertActions } from "./period-brief-insert-actions";

const insertNotePeriodBrief = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    insertNotePeriodBrief: (...args: unknown[]) => insertNotePeriodBrief(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function renderActions(part: Extract<MessagePart, { type: "period_brief_insert" }>) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <PeriodBriefInsertActions part={part} />
    </QueryClientProvider>,
  );
}

describe("PeriodBriefInsertActions", () => {
  beforeEach(() => {
    insertNotePeriodBrief.mockReset();
    insertNotePeriodBrief.mockResolvedValue({ mode: "append", title: "工作介绍 本周" });
  });

  it("offers insert-below and insert-child buttons", () => {
    renderActions({ type: "period_brief_insert", ref_id: "run-1" });
    expect(screen.getByTestId("period-brief-insert-below")).toHaveTextContent("Insert below note");
    expect(screen.getByTestId("period-brief-insert-child")).toHaveTextContent("Insert as child note");
  });

  it("posts append when the below button is clicked", async () => {
    const user = userEvent.setup();
    renderActions({ type: "period_brief_insert", ref_id: "run-1" });
    await user.click(screen.getByTestId("period-brief-insert-below"));
    await waitFor(() => {
      expect(insertNotePeriodBrief).toHaveBeenCalledWith("run-1", { mode: "append" });
    });
    expect(screen.getByTestId("period-brief-insert-below")).toBeEnabled();
    expect(screen.getByTestId("period-brief-insert-child")).toBeEnabled();
  });

  it("keeps both buttons enabled after a previous insert", () => {
    renderActions({
      type: "period_brief_insert",
      ref_id: "run-1",
      selected_option_id: "child",
    });
    expect(screen.getByTestId("period-brief-insert-below")).toBeEnabled();
    expect(screen.getByTestId("period-brief-insert-child")).toBeEnabled();
  });
});
