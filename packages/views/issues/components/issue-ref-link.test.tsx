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

vi.mock("./issue-chip", () => ({
  useResolvedIssue: () => null,
}));

describe("IssueRefLink navigation context", () => {
  it("keeps the prior plain issue href without a NavigationProvider", () => {
    render(<IssueRefLink issueId="issue-1" text="ACME-1" />);

    expect(screen.getByRole("link", { name: "ACME-1" })).toHaveAttribute(
      "href",
      "/acme/issues/issue-1",
    );
  });

  it("adds a bounded channel return intent when navigation context is present", () => {
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
