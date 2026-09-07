/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { noteKeys } from "@multica/core/notes/queries";
import type { MessagePart, NotePage } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { PeriodBriefInsertActions, PeriodBriefInsertProposeCard } from "./period-brief-insert-actions";

const insertNotePeriodBrief = vi.fn();
const listNotePages = vi.fn();
const getNotePage = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    insertNotePeriodBrief: (...args: unknown[]) => insertNotePeriodBrief(...args),
    listNotePages: (...args: unknown[]) => listNotePages(...args),
    getNotePage: (...args: unknown[]) => getNotePage(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function notePage(overrides: Partial<NotePage> = {}): NotePage {
  return {
    id: "page-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "user-1",
    title: "Issuing note",
    content: "",
    sort_key: "1",
    share_user_ids: [],
    can_manage_shares: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    deleted_at: null,
    ...overrides,
  };
}

function renderActions(part: Extract<MessagePart, { type: "period_brief_insert" }>, qc?: QueryClient) {
  const client = qc ?? new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return {
    qc: client,
    ...renderWithI18n(
      <QueryClientProvider client={client}>
        <PeriodBriefInsertActions part={part} />
      </QueryClientProvider>,
    ),
  };
}

describe("PeriodBriefInsertActions", () => {
  beforeEach(() => {
    insertNotePeriodBrief.mockReset();
    listNotePages.mockReset();
    getNotePage.mockReset();
    insertNotePeriodBrief.mockResolvedValue({
      mode: "append",
      title: "工作介绍 本周",
      page_id: "page-1",
      page_title: "Issuing note",
    });
    listNotePages.mockResolvedValue({
      pages: [
        notePage({ content: "Existing body" }),
        notePage({ id: "page-9", title: "Weekly log" }),
      ],
    });
    getNotePage.mockResolvedValue(
      notePage({
        content: "Existing body\n\n## 工作介绍 本周\n\nDone.",
        updated_at: "2026-09-04T00:00:01Z",
      }),
    );
  });

  it("offers insert-below and insert-child buttons", () => {
    renderActions({ type: "period_brief_insert", ref_id: "run-1" });
    expect(screen.getByTestId("period-brief-insert-below")).toHaveTextContent("Insert below note");
    expect(screen.getByTestId("period-brief-insert-child")).toHaveTextContent("Insert as child note");
    expect(screen.getByTestId("period-brief-insert-target")).toBeTruthy();
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

  it("puts the inserted page body into the open-note cache", async () => {
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    qc.setQueryData(noteKeys.detail("ws-1", "page-1"), notePage({ content: "Existing body" }));
    renderActions(
      {
        type: "period_brief_insert",
        ref_id: "run-1",
        source_page_id: "page-1",
        label: "Issuing note",
      },
      qc,
    );
    await user.click(screen.getByTestId("period-brief-insert-below"));
    await waitFor(() => {
      expect(getNotePage).toHaveBeenCalledWith("page-1");
    });
    expect(qc.getQueryData<NotePage>(noteKeys.detail("ws-1", "page-1"))?.content).toContain(
      "## 工作介绍 本周",
    );
  });

  it("posts onto a chosen page", async () => {
    const user = userEvent.setup();
    renderActions({
      type: "period_brief_insert",
      ref_id: "run-1",
      source_page_id: "page-1",
      label: "Issuing note",
    });
    await user.click(screen.getByTestId("period-brief-insert-target"));
    await waitFor(() => {
      expect(screen.getByTestId("period-brief-insert-page-page-9")).toBeTruthy();
    });
    await user.click(screen.getByTestId("period-brief-insert-page-page-9"));
    await user.click(screen.getByTestId("period-brief-insert-below"));
    await waitFor(() => {
      expect(insertNotePeriodBrief).toHaveBeenCalledWith("run-1", {
        mode: "append",
        target_page_id: "page-9",
      });
    });
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

describe("PeriodBriefInsertProposeCard", () => {
  beforeEach(() => {
    insertNotePeriodBrief.mockReset();
    listNotePages.mockReset();
    insertNotePeriodBrief.mockResolvedValue({
      mode: "append",
      title: "工作介绍 本周",
      page_id: "page-9",
      page_title: "Weekly log",
    });
    listNotePages.mockResolvedValue({
      pages: [notePage({ id: "page-9", title: "Weekly log" })],
    });
    getNotePage.mockResolvedValue(
      notePage({
        id: "page-9",
        title: "Weekly log",
        content: "Log\n\n## 工作介绍 本周\n\nDone.",
      }),
    );
  });

  it("confirms a speech-resolved target instead of inserting immediately", async () => {
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <PeriodBriefInsertProposeCard
          propose={{ runId: "run-1", targetPageId: "page-9", mode: "append", label: "Weekly log" }}
          sourcePageId="page-1"
        />
      </QueryClientProvider>,
    );
    expect(insertNotePeriodBrief).not.toHaveBeenCalled();
    expect(screen.getByTestId("period-brief-insert-propose")).toHaveTextContent(
      "Insert the report below “Weekly log”?",
    );
    await user.click(screen.getByTestId("period-brief-insert-propose-confirm"));
    await waitFor(() => {
      expect(insertNotePeriodBrief).toHaveBeenCalledWith("run-1", {
        mode: "append",
        target_page_id: "page-9",
      });
    });
  });
});
