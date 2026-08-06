import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { NavigationProvider } from "../../navigation/context";
import { IssueRefLink } from "./issue-ref-link";

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    issueDetail: (issueId: string) => `/acme/issues/${issueId}`,
    channels: () => "/acme/channels",
  }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "Unknown",
    getActorAvatarUrl: () => null,
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

type ResolvedIssueStub = {
  id: string;
  identifier?: string;
  title?: string;
  status?: string;
};

const mockUseResolvedIssue = vi.hoisted(() =>
  vi.fn((_issueId: string): ResolvedIssueStub | null => null),
);

vi.mock("./issue-chip", () => ({
  useResolvedIssue: (issueId: string) => mockUseResolvedIssue(issueId),
}));

describe("IssueRefLink navigation context", () => {
  it("keeps the prior plain issue href without a NavigationProvider", () => {
    mockUseResolvedIssue.mockReturnValue(null);
    render(<IssueRefLink issueId="issue-1" text="ACME-1" />);

    expect(screen.getByRole("link", { name: "ACME-1" })).toHaveAttribute(
      "href",
      "/acme/issues/issue-1",
    );
  });

  it("adds a bounded channel return intent when navigation context is present", () => {
    mockUseResolvedIssue.mockReturnValue(null);
    render(
      <NavigationProvider
        value={{
          push: vi.fn(),
          replace: vi.fn(),
          back: vi.fn(),
          pathname: "/acme/channels/channel-1",
          searchParams: new URLSearchParams("message=message-1"),
          getShareableUrl: (path) => path,
        }}
      >
        <IssueRefLink issueId="issue-1" text="ACME-1" />
      </NavigationProvider>,
    );

    expect(screen.getByRole("link", { name: "ACME-1" })).toHaveAttribute(
      "href",
      "/acme/issues/issue-1?returnTo=%2Facme%2Fchannels%2Fchannel-1%3Fmessage%3Dmessage-1",
    );
  });

  it("anchors the return intent to the rendered Messages row", () => {
    mockUseResolvedIssue.mockReturnValue(null);
    render(
      <NavigationProvider
        value={{
          push: vi.fn(),
          replace: vi.fn(),
          back: vi.fn(),
          pathname: "/acme/channels/channel-1",
          searchParams: new URLSearchParams("message=another-row"),
          getShareableUrl: (path) => path,
        }}
      >
        <IssueRefLink issueId="issue-1" text="ACME-1" sourceMessageId="source-row" />
      </NavigationProvider>,
    );

    expect(screen.getByRole("link", { name: "ACME-1" })).toHaveAttribute(
      "href",
      "/acme/issues/issue-1?returnTo=%2Facme%2Fchannels%2Fchannel-1%3Fmessage%3Dsource-row",
    );
  });
});

describe("IssueRefLink title-first (LRM-508)", () => {
  it("rewrites author LRM-xxx ink to the live issue title once resolved", () => {
    mockUseResolvedIssue.mockReturnValue({
      id: "issue-1",
      identifier: "LRM-487",
      title: "Soft-ask density",
      status: "todo",
    });

    render(<IssueRefLink issueId="issue-1" text="LRM-487" />);

    const link = screen.getByRole("link", { name: "Soft-ask density" });
    expect(link).toBeInTheDocument();
    // Identifier stays out of the main-line link (peek only).
    expect(link).not.toHaveTextContent("LRM-487");
  });

  it("keeps author text as interim when the issue has not resolved yet", () => {
    mockUseResolvedIssue.mockReturnValue(null);
    render(<IssueRefLink issueId="issue-1" text="LRM-487" />);
    expect(screen.getByRole("link", { name: "LRM-487" })).toBeInTheDocument();
  });
});
