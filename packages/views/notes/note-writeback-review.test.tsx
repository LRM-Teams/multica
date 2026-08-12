/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { NotePage, NoteWriteback } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteWritebackReview } from "./note-writeback-review";

const listNotePageWritebacks = vi.fn();
const acceptNotePageWriteback = vi.fn();
const rejectNotePageWriteback = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    listNotePageWritebacks: (...args: unknown[]) => listNotePageWritebacks(...args),
    acceptNotePageWriteback: (...args: unknown[]) => acceptNotePageWriteback(...args),
    rejectNotePageWriteback: (...args: unknown[]) => rejectNotePageWriteback(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    issues: () => "/acme/issues",
  }),
}));

vi.mock("../navigation", () => ({
  AppLink: ({ href, children, ...props }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...props}>{children}</a>
  ),
}));

const page: NotePage = {
  id: "page-1",
  workspace_id: "ws-1",
  parent_id: null,
  owner_user_id: "user-1",
  title: "Daily",
  content: "Original body",
  sort_key: "1",
  share_user_ids: [],
  can_manage_shares: true,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  deleted_at: null,
};

function pendingWriteback(overrides: Partial<NoteWriteback> = {}): NoteWriteback {
  return {
    id: "wb-1",
    workspace_id: "ws-1",
    page_id: "page-1",
    action: "append",
    content: "Appended summary",
    evidence: [{ type: "issue", id: "issue-1", label: "MUL-1" }],
    status: "pending",
    created_by_type: "member",
    created_by_id: "user-1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function renderReview(onAppliedContent = vi.fn()) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    onAppliedContent,
    ...renderWithI18n(
      <QueryClientProvider client={qc}>
        <NoteWritebackReview
          page={page}
          currentContent={page.content}
          onAppliedContent={onAppliedContent}
        />
      </QueryClientProvider>,
    ),
  };
}

describe("NoteWritebackReview", () => {
  beforeEach(() => {
    listNotePageWritebacks.mockReset();
    acceptNotePageWriteback.mockReset();
    rejectNotePageWriteback.mockReset();
  });

  it("renders nothing when there are no pending writebacks", async () => {
    listNotePageWritebacks.mockResolvedValue({ writebacks: [] });
    renderReview();
    await waitFor(() => expect(listNotePageWritebacks).toHaveBeenCalled());
    expect(screen.queryByTestId("note-writeback-review")).not.toBeInTheDocument();
  });

  it("shows evidence links and accepts a writeback", async () => {
    const user = userEvent.setup();
    listNotePageWritebacks.mockResolvedValue({ writebacks: [pendingWriteback()] });
    acceptNotePageWriteback.mockResolvedValue(pendingWriteback({ status: "applied" }));
    const { onAppliedContent } = renderReview();

    expect(await screen.findByTestId("note-writeback-review")).toBeInTheDocument();
    const evidence = screen.getByTestId("note-writeback-evidence");
    expect(evidence.querySelector('a[href="/acme/issues/issue-1"]')).toBeTruthy();

    await user.click(screen.getByTestId("note-writeback-accept"));
    await waitFor(() => expect(acceptNotePageWriteback).toHaveBeenCalledWith("wb-1"));
    expect(onAppliedContent).toHaveBeenCalledWith("Original body\n\nAppended summary");
  });

  it("rejects without changing content", async () => {
    const user = userEvent.setup();
    listNotePageWritebacks.mockResolvedValue({ writebacks: [pendingWriteback()] });
    rejectNotePageWriteback.mockResolvedValue(pendingWriteback({ status: "rejected" }));
    const { onAppliedContent } = renderReview();

    await screen.findByTestId("note-writeback-review");
    await user.click(screen.getByTestId("note-writeback-reject"));
    await waitFor(() => expect(rejectNotePageWriteback).toHaveBeenCalledWith("wb-1"));
    expect(onAppliedContent).not.toHaveBeenCalled();
  });
});
