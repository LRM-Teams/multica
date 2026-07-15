import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MessagePart } from "@multica/core/types";
import { InlineReferenceContent } from "./inline-reference-content";

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (state: { cdnDomain: string }) => unknown) =>
    selector({ cdnDomain: "" }),
}));
vi.mock("@multica/core/api", () => ({ api: { getBaseUrl: () => "" } }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } | null }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, _id: string, fallback?: string) => fallback ?? "Alice",
  }),
}));
vi.mock("../issues/components/issue-mention-card", () => ({
  IssueMentionCard: ({ issueId }: { issueId: string }) => <span>{issueId}</span>,
}));
vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspaceSlug: () => "acme",
    useRequiredWorkspaceSlug: () => "acme",
    useCurrentWorkspace: () => null,
    useWorkspacePaths: () => ({
      ...actual.paths.workspace("acme"),
      projectDetail: (projectId: string) => `/projects/${projectId}`,
    }),
  };
});
vi.mock("../navigation/app-link", () => ({
  AppLink: ({ href, children, className }: { href: string; children: ReactNode; className?: string }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));
vi.mock("./actor-profile-popover", () => ({
  ActorProfileTrigger: ({
    memberType,
    memberId,
    children,
  }: {
    memberType: string;
    memberId: string;
    children: ReactNode;
  }) => (
    <span data-testid="actor-profile-trigger" data-member-type={memberType} data-member-id={memberId}>
      {children}
    </span>
  ),
}));

function mention(start: number, end: number): MessagePart {
  return {
    type: "reference",
    ref_type: "mention",
    ref_subtype: "member",
    ref_id: "user-1",
    label: "@Alice",
    content_start_utf16: start,
    content_end_utf16: end,
  } as MessagePart;
}
function issueRef(start: number, end: number): MessagePart {
  return {
    type: "reference",
    ref_type: "issue-ref",
    ref_subtype: "issue",
    ref_id: "issue-uuid",
    label: "MUL-9",
    content_start_utf16: start,
    content_end_utf16: end,
  } as MessagePart;
}

describe("InlineReferenceContent (#463 projector consumer)", () => {
  it("renders a structured mention as the hover-card token — restores the hover the bare-@ window dropped", () => {
    // "hey @Alice now" — @Alice at [4,10)
    render(<InlineReferenceContent content="hey @Alice now" parts={[mention(4, 10)]} />);
    const trigger = screen.getByTestId("actor-profile-trigger");
    expect(trigger).toHaveAttribute("data-member-id", "user-1");
    expect(trigger).toHaveTextContent("@Alice");
  });

  it("renders an issue-ref as a link to the issue detail, showing the span substring verbatim", () => {
    // "see #MUL-9 pls" — #MUL-9 at [4,10)
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);
    const link = screen.getByText("#MUL-9").closest("a");
    expect(link).not.toBeNull();
    expect(link).toHaveAttribute("href", expect.stringContaining("issues/issue-uuid"));
  });

  it("renders the author's span substring verbatim — never synthesizes a `#` prefix (#467/#600 content-as-is)", () => {
    // Author wrote bare `MUL-123` (no #) — it must render as `MUL-123`, not `#MUL-123`.
    render(<InlineReferenceContent content="fix MUL-123 now" parts={[issueRef(4, 11)]} />);
    expect(screen.getByText("MUL-123")).toBeInTheDocument();
    expect(screen.queryByText("#MUL-123")).toBeNull();
  });

  it("non-interactive mode: mention is styled text, no hover card / nested link", () => {
    // "from @Alice" — @Alice at [5,11)
    render(<InlineReferenceContent content="from @Alice" parts={[mention(5, 11)]} interactive={false} />);
    expect(screen.queryByTestId("actor-profile-trigger")).toBeNull();
    expect(screen.getByText("@Alice")).toBeInTheDocument();
  });

  it("no anchored references → plain text, no token", () => {
    render(<InlineReferenceContent content="just text" parts={[]} />);
    expect(screen.getByText("just text")).toBeInTheDocument();
    expect(screen.queryByTestId("actor-profile-trigger")).toBeNull();
  });

  it("renders text runs inline so a mention never breaks the line (#601 block regression)", () => {
    // "hey @Alice check" — @Alice at [4,10); runs "hey " + token + " check".
    // Pre-fix each text run rendered as a block <div class="markdown-content">,
    // forcing the inline mention onto its own line. The `inline` render mode must
    // put text runs in an inline <span class="markdown-content-inline"> instead.
    const { container } = render(
      <InlineReferenceContent content="hey @Alice check" parts={[mention(4, 10)]} />,
    );
    expect(container.querySelector("div.markdown-content")).toBeNull();
    expect(container.querySelector("span.markdown-content-inline")).not.toBeNull();
    expect(container).toHaveTextContent("hey @Alice check");
  });
});
