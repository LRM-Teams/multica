// @vitest-environment happy-dom
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IssueChip } from "./issue-chip";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/issues/queries", () => ({
  issueListOptions: () => ({
    queryKey: ["issues", "ws-1", "list"],
    queryFn: async () => [],
    enabled: true,
  }),
  issueDetailOptions: (_wsId: string, id: string) => ({
    queryKey: ["issues", "ws-1", "detail", id],
    queryFn: async () => {
      const err = Object.assign(new Error("not found"), { status: 404 });
      throw err;
    },
    enabled: true,
    retry: false,
  }),
}));

describe("IssueChip inaccessible degrade", () => {
  it("shows unresolvedLabel and does not render a leaked title", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={qc}>
        <IssueChip
          issueId="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
          fallbackLabel="MUL-99"
          unresolvedLabel="Unavailable"
        />
      </QueryClientProvider>,
    );

    expect(await screen.findByText("Unavailable")).toBeTruthy();
    expect(screen.queryByText("Secret leaked title")).toBeNull();
    expect(document.querySelector('[data-issue-unresolved="true"]')).toBeTruthy();
  });
});
