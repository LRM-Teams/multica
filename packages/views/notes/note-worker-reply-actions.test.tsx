/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteWorkerReplyActions } from "./note-worker-reply-actions";

const getNotePage = vi.fn();
const updateNotePage = vi.fn();
const createNotePage = vi.fn();
const push = vi.fn();

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

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    noteDetail: (id: string) => `/notes/${id}`,
  }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push }),
}));

function renderActions(message: ChannelMessage) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <NoteWorkerReplyActions message={message} pageId="page-1" />
    </QueryClientProvider>,
  );
}

describe("NoteWorkerReplyActions", () => {
  beforeEach(() => {
    getNotePage.mockReset();
    updateNotePage.mockReset();
    createNotePage.mockReset();
    push.mockReset();
    getNotePage.mockResolvedValue({
      id: "page-1",
      title: "Parent",
      content: "Original body",
    });
    updateNotePage.mockImplementation(async (id: string, data: { content?: string; title?: string }) => ({
      id,
      title: data.title ?? "Parent",
      content: data.content ?? "",
    }));
    createNotePage.mockResolvedValue({ id: "child-1", title: "Ship it", content: "" });
  });

  it("inserts the reply below the original note with a titled section", async () => {
    const user = userEvent.setup();
    renderActions({
      id: "a1",
      type: "agent",
      content: "Ship it\n\nDetails here.",
    } as ChannelMessage);

    await user.click(screen.getByTestId("note-worker-insert-below"));

    await waitFor(() => {
      expect(updateNotePage).toHaveBeenCalledWith("page-1", {
        content: "Original body\n\n## Ship it\n\nShip it\n\nDetails here.",
      });
    });
    expect(createNotePage).not.toHaveBeenCalled();
  });

  it("creates a top-level note when no page is specified", async () => {
    const user = userEvent.setup();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    createNotePage.mockResolvedValue({ id: "new-1", title: "Ship it", content: "" });
    renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteWorkerReplyActions
          message={{ id: "a1", type: "agent", content: "Ship it\n\nDetails here." } as ChannelMessage}
        />
      </QueryClientProvider>,
    );

    expect(screen.queryByTestId("note-worker-insert-below")).toBeNull();
    await user.click(screen.getByTestId("note-worker-create-note"));

    await waitFor(() => {
      expect(createNotePage).toHaveBeenCalledWith({ title: "Ship it" });
      expect(updateNotePage).toHaveBeenCalledWith("new-1", {
        content: "Ship it\n\nDetails here.",
      });
    });
  });

  it("creates a child note and writes the reply into it", async () => {
    const user = userEvent.setup();
    renderActions({
      id: "a1",
      type: "agent",
      content: "Ship it\n\nDetails here.",
    } as ChannelMessage);

    await user.click(screen.getByTestId("note-worker-create-child"));

    await waitFor(() => {
      expect(createNotePage).toHaveBeenCalledWith({
        parent_id: "page-1",
        title: "Ship it",
      });
      expect(updateNotePage).toHaveBeenCalledWith("child-1", {
        content: "Ship it\n\nDetails here.",
      });
    });
  });
});
