/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../../test/i18n";
import { NoteChatInsertActions } from "./note-chat-insert-actions";

const getNotePage = vi.fn();
const updateNotePage = vi.fn();
const createNotePage = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    getNotePage: (...args: unknown[]) => getNotePage(...args),
    updateNotePage: (...args: unknown[]) => updateNotePage(...args),
    createNotePage: (...args: unknown[]) => createNotePage(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function renderActions(text: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <NoteChatInsertActions pageId="page-1" text={text} />
    </QueryClientProvider>,
    { locale: "zh-Hans" },
  );
}

describe("NoteChatInsertActions", () => {
  beforeEach(() => {
    getNotePage.mockReset();
    updateNotePage.mockReset();
    createNotePage.mockReset();
    getNotePage.mockResolvedValue({ id: "page-1", content: "原文" });
    updateNotePage.mockImplementation(async (id: string, body: { content?: string }) => ({
      id,
      ...body,
    }));
    createNotePage.mockResolvedValue({ id: "child-1", title: "提纲" });
  });

  it("shows insert-below and insert-child buttons", () => {
    renderActions("# 提纲\n\n- 待办");
    expect(screen.getByTestId("note-chat-insert-below")).toHaveTextContent("插入笔记下面");
    expect(screen.getByTestId("note-chat-insert-child")).toHaveTextContent("插入子笔记");
  });

  it("inserts the fenced markdown below the current page", async () => {
    const user = userEvent.setup();
    renderActions("我不能直接插入。\n\n```markdown\n# 提纲\n\n- 待办\n```");
    await user.click(screen.getByTestId("note-chat-insert-below"));
    await waitFor(() => {
      expect(updateNotePage).toHaveBeenCalledWith("page-1", {
        content: "原文\n\n# 提纲\n\n- 待办",
      });
    });
  });

  it("creates a child note from the fenced markdown", async () => {
    const user = userEvent.setup();
    renderActions("```markdown\n# 提纲\n\n- 待办\n```");
    await user.click(screen.getByTestId("note-chat-insert-child"));
    await waitFor(() => {
      expect(createNotePage).toHaveBeenCalledWith({ parent_id: "page-1", title: "提纲" });
      expect(updateNotePage).toHaveBeenCalledWith("child-1", { content: "# 提纲\n\n- 待办" });
    });
  });
});
