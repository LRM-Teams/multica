/**
 * @vitest-environment jsdom
 *
 * B4 (#242) — Reaction 4-carrier consistency: the "never wakes an agent"
 * invariant for the issue carrier. Issue-detail exposes two reaction surfaces —
 * issue-level reactions (`useToggleIssueReaction`) and per-comment reactions
 * (`useToggleCommentReaction`). Both must go through the dedicated reaction
 * endpoints and MUST NOT call `createComment`, which is the wake-producing
 * dispatch on the issue timeline. A regression that posted a comment to toggle
 * a reaction would wake every subscribed agent on the issue.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type { IssueReaction, Reaction } from "../types";
import { useToggleIssueReaction, useToggleCommentReaction } from "./mutations";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function makeApi() {
  return {
    addIssueReaction: vi.fn().mockResolvedValue({
      id: "ir-1",
      issue_id: "issue-1",
      actor_type: "member",
      actor_id: "user-1",
      emoji: "👍",
      created_at: "2026-07-04T00:00:00Z",
    } satisfies IssueReaction),
    removeIssueReaction: vi.fn().mockResolvedValue(undefined),
    addReaction: vi.fn().mockResolvedValue({
      id: "r-1",
      comment_id: "comment-1",
      actor_type: "member",
      actor_id: "user-1",
      emoji: "👍",
      created_at: "2026-07-04T00:00:00Z",
    } satisfies Reaction),
    removeReaction: vi.fn().mockResolvedValue(undefined),
    // The wake-producing call — must never fire on a reaction.
    createComment: vi.fn(),
  };
}

describe("issue reaction never wakes an agent", () => {
  let qc: QueryClient;
  let api: ReturnType<typeof makeApi>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    api = makeApi();
    setApiInstance(api as unknown as ApiClient);
  });

  afterEach(() => {
    setApiInstance(undefined as unknown as ApiClient);
    vi.clearAllMocks();
  });

  it("adds an issue reaction through the reaction endpoint, not a comment", async () => {
    const { result } = renderHook(() => useToggleIssueReaction("issue-1"), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({ emoji: "👍", existing: undefined });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.addIssueReaction).toHaveBeenCalledWith("issue-1", "👍");
    expect(api.createComment).not.toHaveBeenCalled();
  });

  it("removes an issue reaction through the reaction endpoint, not a comment", async () => {
    const existing: IssueReaction = {
      id: "ir-9",
      issue_id: "issue-1",
      actor_type: "member",
      actor_id: "user-1",
      emoji: "👍",
      created_at: "2026-07-04T00:00:00Z",
    };
    const { result } = renderHook(() => useToggleIssueReaction("issue-1"), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({ emoji: "👍", existing });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.removeIssueReaction).toHaveBeenCalledWith("issue-1", "👍");
    expect(api.createComment).not.toHaveBeenCalled();
  });

  it("adds a comment reaction through the reaction endpoint, not a comment", async () => {
    const { result } = renderHook(() => useToggleCommentReaction("issue-1"), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({ commentId: "comment-1", emoji: "👍", existing: undefined });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.addReaction).toHaveBeenCalledWith("comment-1", "👍");
    expect(api.createComment).not.toHaveBeenCalled();
  });

  it("removes a comment reaction through the reaction endpoint, not a comment", async () => {
    const existing: Reaction = {
      id: "r-9",
      comment_id: "comment-1",
      actor_type: "member",
      actor_id: "user-1",
      emoji: "👍",
      created_at: "2026-07-04T00:00:00Z",
    };
    const { result } = renderHook(() => useToggleCommentReaction("issue-1"), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({ commentId: "comment-1", emoji: "👍", existing });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.removeReaction).toHaveBeenCalledWith("comment-1", "👍");
    expect(api.createComment).not.toHaveBeenCalled();
  });
});
