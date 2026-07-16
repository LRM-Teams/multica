import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
// The real hover popup only opens on a true pointer hover (verified on a real
// machine, not jsdom). Mocking the primitive keeps these tests on what this
// component actually decides: WHETHER to wrap the token in a peek, and what the
// peek renders from the server-fed part.
vi.mock("@multica/ui/components/ui/hover-card", () => ({
  HoverCard: ({ children }: { children: ReactNode }) => (
    <span data-testid="issue-hover-card">{children}</span>
  ),
  HoverCardTrigger: ({ children }: { children: ReactNode }) => <span>{children}</span>,
  HoverCardContent: ({ children }: { children: ReactNode }) => (
    <span data-testid="issue-hover-content">{children}</span>
  ),
}));

// The peek's data source: the LIVE issue, not the part's snapshot (#504).
// Tests set this per-case; beforeEach resets it so an unresolved issue is the
// default (otherwise a leftover would make the verbatim tests see the identifier
// twice — once as the token, once inside the peek).
let resolvedIssue:
  | {
      id: string;
      title: string;
      status: string;
      priority?: string;
      assignee_type?: string | null;
      assignee_id?: string | null;
      project_id?: string | null;
    }
  | undefined;

vi.mock("../issues/components/issue-chip", () => ({
  useResolvedIssue: () => resolvedIssue,
}));

// ProjectChip resolves the project name itself via TanStack; stub it so these
// tests stay on what the peek decides rather than dragging in a QueryClient.
vi.mock("../projects/components/project-chip", () => ({
  ProjectChip: ({ projectId }: { projectId: string }) => (
    <span data-testid="project-chip">{projectId}</span>
  ),
}));

vi.mock("../issues/components/priority-icon", () => ({
  PriorityIcon: ({ priority }: { priority: string }) => (
    <svg data-testid="priority-icon" data-priority={priority} />
  ),
}));

vi.mock("../issues/components/status-icon", () => ({
  StatusIcon: ({ status }: { status: string }) => (
    <svg data-testid="status-icon" data-status={status} />
  ),
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
  beforeEach(() => {
    resolvedIssue = undefined;
  });

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

  it("peeks with the LIVE issue title + status (#469)", () => {
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "in_progress" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByTestId("issue-hover-content")).toBeInTheDocument();
    expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    // The token itself stays a plain clickable link — status lives in the peek.
    // (The identifier appears twice: once as the token, once inside the peek.)
    expect(screen.getAllByText("#MUL-9").some((el) => el.closest("a"))).toBe(true);
  });

  // THE regression for #504. The persisted part is anchor/identity only; all
  // mutable entity state comes from the live issue query.
  it("renders the LIVE issue state from the entity query (#504)", () => {
    resolvedIssue = { id: "issue-uuid", title: "Current title", status: "in_progress" };

    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByText("Current title")).toBeInTheDocument();
    expect(screen.getByTestId("status-icon")).toHaveAttribute("data-status", "in_progress");
  });

  it("peeks the four properties Frank named: status · priority · assignee · project (#504)", () => {
    resolvedIssue = {
      id: "issue-uuid",
      title: "Fix the login bug",
      status: "in_progress",
      priority: "high",
      assignee_type: "member",
      assignee_id: "user-1",
      project_id: "proj-7",
    };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByTestId("status-icon")).toHaveAttribute("data-status", "in_progress");
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByTestId("priority-icon")).toHaveAttribute("data-priority", "high");
    expect(screen.getByText("High")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument(); // assignee resolved live by id
    expect(screen.getByTestId("project-chip")).toHaveTextContent("proj-7");
  });

  it("omits each property independently when absent — never a placeholder (#504)", () => {
    // Title + status only: no priority, unassigned, no project.
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "todo" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByText("Todo")).toBeInTheDocument();
    expect(screen.queryByTestId("priority-icon")).toBeNull();
    expect(screen.queryByTestId("project-chip")).toBeNull();
  });

  it("treats priority 'none' as absent rather than drawing a 'None' row (#504)", () => {
    resolvedIssue = {
      id: "issue-uuid",
      title: "Fix the login bug",
      status: "todo",
      priority: "none",
    };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.queryByTestId("priority-icon")).toBeNull();
    expect(screen.queryByText("None")).toBeNull();
  });

  it("degrades to a plain clickable token while the issue is unresolved (#469)", () => {
    // Loading / deleted / other workspace / no permission → no card.
    resolvedIssue = undefined;
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.queryByTestId("issue-hover-card")).toBeNull();
    expect(screen.getByText("#MUL-9").closest("a")).not.toBeNull();
  });

  it("peeks with title only when the live issue has no drawable status (#469 partial)", () => {
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "not_a_real_status" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    // Unknown status is ignored (no bogus icon) but the title still peeks.
    expect(screen.getByTestId("issue-hover-content")).toBeInTheDocument();
    expect(screen.getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.queryByTestId("status-icon")).toBeNull();
  });

  it("peeks with status only when the live issue has no title (#469 partial)", () => {
    resolvedIssue = { id: "issue-uuid", title: "", status: "done" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByTestId("issue-hover-content")).toBeInTheDocument();
    expect(screen.getByTestId("status-icon")).toHaveAttribute("data-status", "done");
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
