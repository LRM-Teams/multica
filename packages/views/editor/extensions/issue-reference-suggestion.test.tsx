// @vitest-environment node
import { describe, it, expect, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import { issueKeys } from "@multica/core/issues/queries";
import type { Issue, ListIssuesCache } from "@multica/core/types";
import type { MentionItem } from "./mention-suggestion";
import { createIssueReferenceSuggestion } from "./issue-reference-suggestion";

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    searchIssues: vi.fn(),
  },
}));

function fakeIssue(overrides: Partial<Issue>): Issue {
  return {
    id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    workspace_id: "ws-1",
    number: 1,
    identifier: "MUL-1",
    title: "Ship notes issue refs",
    status: "todo",
    priority: "none",
    creator_type: "member",
    creator_id: "u1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  } as Issue;
}

function fakeQc(issues: Issue[]): QueryClient {
  const cache: ListIssuesCache = {
    byStatus: {
      todo: { issues, total: issues.length },
    },
  };
  const map = new Map<string, unknown>();
  // Partial key match: real list queries append filter segments after issueKeys.list(wsId).
  map.set(JSON.stringify([...issueKeys.list("ws-1"), {}]), cache);
  return {
    getQueryData: (key: readonly unknown[]) => map.get(JSON.stringify(key)),
    getQueriesData: ({ queryKey }: { queryKey: readonly unknown[] }) => {
      return [...map.entries()]
        .filter(([k]) => {
          const parsed = JSON.parse(k) as unknown[];
          return queryKey.every((part, i) => parsed[i] === part);
        })
        .map(([k, v]) => [JSON.parse(k), v]);
    },
  } as unknown as QueryClient;
}

describe("createIssueReferenceSuggestion", () => {
  it("filters cached issues by identifier/title", () => {
    const qc = fakeQc([
      fakeIssue({ id: "a1", identifier: "MUL-1", title: "Alpha" }),
      fakeIssue({ id: "a2", identifier: "MUL-2", title: "Beta notes bridge" }),
    ]);
    const config = createIssueReferenceSuggestion(qc);
    const items = config.items!({ query: "bridge", editor: {} as never }) as MentionItem[];
    expect(items).toEqual([
      expect.objectContaining({ id: "a2", label: "MUL-2", type: "issue", description: "Beta notes bridge" }),
    ]);
  });

  it("inserts issueReference with identifier label and null title", () => {
    const qc = fakeQc([fakeIssue({ id: "a1", identifier: "MUL-1", title: "Secret title" })]);
    const config = createIssueReferenceSuggestion(qc);
    const insertContentAt = vi.fn().mockReturnThis();
    const focus = vi.fn().mockReturnThis();
    const run = vi.fn();
    focus.mockReturnValue({ insertContentAt });
    insertContentAt.mockReturnValue({ run });
    const editor = { chain: vi.fn(() => ({ focus, insertContentAt, run })) } as never;

    config.command!({
      editor,
      range: { from: 1, to: 2 },
      props: { id: "a1", label: "MUL-1", type: "issue", description: "Secret title" },
    });

    expect(insertContentAt).toHaveBeenCalledWith(
      { from: 1, to: 2 },
      {
        type: "issueReference",
        attrs: { id: "a1", label: "MUL-1", title: null },
      },
    );
  });

  it("explicitly allows the suggestion (not deep-merge disabled)", () => {
    const config = createIssueReferenceSuggestion(fakeQc([]));
    expect(config.allow?.({} as never)).toBe(true);
  });
});
