import type { ReactNode } from "react";
import { render, screen, within } from "@testing-library/react";
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
vi.mock("./use-resolved-actor-identity", () => ({
  useResolvedActorIdentity: (actorId: string | undefined, mentionType: string | null) => {
    if (!actorId || !mentionType) return { displayName: null, avatarUrl: null };
    if (mentionType === "agent") return { displayName: "Bot", avatarUrl: null };
    return { displayName: "Alice", avatarUrl: null };
  },
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

// The project resolves via TanStack; stub the queries so these tests stay on what
// the peek decides rather than dragging in a QueryClient.
let resolvedProject: { id: string; title: string; icon?: string } | undefined;
// The list query yields an array; the detail fallback yields one project or nothing.
// Keep those shapes distinct — a mock that returns `[]` for the detail query would
// make the "unresolved" case look resolved (an empty array is truthy).
vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: string[] }) =>
    opts.queryKey[0] === "projects"
      ? { data: resolvedProject ? [resolvedProject] : [] }
      : { data: undefined },
}));
vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"] }),
  projectDetailOptions: () => ({ queryKey: ["project"] }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/channels/queries", () => ({
  channelsOptions: () => ({ queryKey: ["channels"] }),
}));

vi.mock("../projects/components/project-icon", () => ({
  ProjectIcon: () => <span data-testid="project-icon" />,
}));

// #517: assignee is an avatar + name, not bare text — the avatar carries no hover
// card of its own (it already lives inside the peek's hover card).
vi.mock("./actor-avatar", () => ({
  ActorAvatar: ({
    actorId,
    enableHoverCard,
    profileLink,
  }: {
    actorId: string;
    enableHoverCard?: boolean;
    profileLink?: boolean;
  }) => (
    <span
      data-testid="assignee-avatar"
      data-actor={actorId}
      data-hover-card={String(Boolean(enableHoverCard))}
      data-profile-link={String(Boolean(profileLink))}
    />
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
// Mirror the real AppLink, which extends AnchorHTMLAttributes and spreads {...props}
// onto the anchor — otherwise this mock would silently swallow `data-ref-source` and
// the assertions below would be testing the mock rather than the component.
vi.mock("../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
    ...rest
  }: {
    href: string;
    children: ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className} {...rest}>
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
function channelRef(
  start: number,
  end: number,
  label = "team-a",
  params?: { message_id?: string; thread_id?: string },
): MessagePart {
  return {
    type: "reference",
    ref_type: "channel-ref",
    ref_id: "channel-uuid",
    label,
    content_start_utf16: start,
    content_end_utf16: end,
    ...(params ? { params } : {}),
  } as MessagePart;
}

describe("InlineReferenceContent (#463 projector consumer)", () => {
  beforeEach(() => {
    resolvedIssue = undefined;
    resolvedProject = undefined;
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

  it("renders a channel-ref as a ChannelChip link, never the raw markdown link syntax (task #912)", () => {
    // The composer always anchors the WHOLE `[team-a](mention://channel/channel-uuid)`
    // markdown link, not a bare identifier — the chip must show the resolved
    // label, never leak the markdown source.
    const raw = "[team-a](mention://channel/channel-uuid)";
    const content = `see ${raw} for context`;
    render(<InlineReferenceContent content={content} parts={[channelRef(4, 4 + raw.length)]} />);
    const chip = screen.getByTestId("channel-chip");
    expect(chip).toHaveTextContent("team-a");
    expect(screen.queryByText(raw, { exact: false })).toBeNull();
    expect(screen.queryByText(/mention:\/\/channel/)).toBeNull();
    const link = chip.closest("a");
    expect(link).toHaveAttribute("href", expect.stringContaining("channels/channel-uuid"));
  });

  it("on a non-interactive surface, renders a channel-ref's resolved label, never the raw markdown link (Wren, PR review)", () => {
    // Same leak class as the interactive test above, but through the OTHER
    // branch: `interactive={false}` skips ChannelRefLink and used to render
    // `text` (the span substring) directly — which for a composer-authored
    // channel-ref is the whole `[Label](mention://channel/<uuid>)` string.
    // The label renders with exactly one `#` (LRM-1153) so this surface reads
    // as a channel the same way the chip and the preview do.
    const raw = "[team-a](mention://channel/channel-uuid)";
    const content = `see ${raw} for context`;
    render(
      <InlineReferenceContent
        content={content}
        parts={[channelRef(4, 4 + raw.length)]}
        interactive={false}
      />,
    );
    expect(screen.getByText("#team-a")).toBeInTheDocument();
    expect(screen.queryByText(raw, { exact: false })).toBeNull();
    expect(screen.queryByText(/mention:\/\/channel/)).toBeNull();
  });

  it("deep-links a #channel:shortId channel-ref to the verified message", () => {
    const content = "see #raft-research:a291584b";
    render(
      <InlineReferenceContent
        content={content}
        parts={[
          channelRef(4, content.length, "raft-research", {
            message_id: "msg-1",
            thread_id: "root-1",
          }),
        ]}
      />,
    );
    expect(screen.getByTestId("channel-chip").closest("a")).toHaveAttribute(
      "href",
      "/acme/channels/channel-uuid?thread=root-1&message=msg-1",
    );
  });

  it("renders a bare `#name` channel-ref as a chip, replacing the hash the author typed (LRM-1153)", () => {
    // The server now anchors bare `#name` prose, so the span is just the token
    // — the chip must replace it whole (its own Hash icon supplies the `#`),
    // leaving no orphan `#` in the surrounding text.
    const content = "巡检增量 #team-a 新反馈";
    render(<InlineReferenceContent content={content} parts={[channelRef(5, 12)]} />);
    const chip = screen.getByTestId("channel-chip");
    expect(chip).toHaveTextContent("team-a");
    expect(chip.textContent).not.toContain("#");
    expect(chip.closest("a")).toHaveAttribute(
      "href",
      expect.stringContaining("channels/channel-uuid"),
    );
  });

  it("on a non-interactive surface, keeps a bare `#name` channel-ref readable as `#name` (LRM-1153)", () => {
    const content = "巡检增量 #team-a 新反馈";
    render(
      <InlineReferenceContent content={content} parts={[channelRef(5, 12)]} interactive={false} />,
    );
    expect(screen.getByText("#team-a")).toBeInTheDocument();
    expect(screen.queryByText("##team-a")).toBeNull();
  });

  it("renders a **bold**-wrapped issue ref as actual bold, not literal asterisks (#635)", () => {
    // Each text run either side of the reference span is markdown-parsed
    // independently, so a lone "**" on each side used to fall through as
    // literal text instead of forming a matched emphasis pair.
    render(<InlineReferenceContent content="Fixed **MUL-123** today" parts={[issueRef(8, 15)]} />);
    const link = screen.getByText("MUL-123").closest("a");
    expect(link).not.toBeNull();
    expect(link?.closest("strong")).not.toBeNull();
    expect(screen.queryByText("**", { exact: false })).toBeNull();
  });

  it("peeks with the LIVE issue title + status (#469)", () => {
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "in_progress" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    const peek = screen.getByTestId("issue-hover-content");
    expect(peek).toBeInTheDocument();
    expect(within(peek).getByText("Fix the login bug")).toBeInTheDocument();
    // LRM-508: resolved main-line ink is the live title (author #MUL-9 is interim only).
    expect(screen.getByRole("link", { name: "Fix the login bug" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "#MUL-9" })).toBeNull();
  });

  // THE regression for #504. The persisted part is anchor/identity only; all
  // mutable entity state comes from the live issue query.
  it("renders the LIVE issue state from the entity query (#504)", () => {
    resolvedIssue = { id: "issue-uuid", title: "Current title", status: "in_progress" };

    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByRole("link", { name: "Current title" })).toBeInTheDocument();
    expect(within(screen.getByTestId("issue-hover-content")).getByText("Current title")).toBeInTheDocument();
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
    resolvedProject = { id: "proj-7", title: "Doudizhu", icon: "🃏" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByTestId("status-icon")).toHaveAttribute("data-status", "in_progress");
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByTestId("priority-icon")).toHaveAttribute("data-priority", "high");
    expect(screen.getByText("High")).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument(); // assignee resolved live by id
    expect(screen.getByTestId("project-icon")).toBeInTheDocument();
    expect(screen.getByText("Doudizhu")).toBeInTheDocument();
  });

  it("marks an anchored reference as provenance `anchor` (#520/#521)", () => {
    // Users must not be able to tell an anchored reference from the linkify
    // fallback — that is the point of #520. So the ONLY thing left that can catch a
    // parser miss (#521) is this attribute: assert every occurrence in a message is
    // `anchor`; a `fallback` means the server skipped one. Invisible to readers,
    // visible to tests — see IssueRefLink's IssueRefSource docs.
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "todo" };
    const { container } = render(
      <InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />,
    );

    const link = container.querySelector("a[data-ref-source]");
    // LRM-508: resolved link ink is the live title; provenance attr is unchanged.
    expect(link).toHaveTextContent("Fix the login bug");
    expect(link).toHaveAttribute("data-ref-source", "anchor");
  });

  it("gives every property the same grammar: marker + label, no chip (#517)", () => {
    // Frank's "有点乱": a bare-text assignee orphaned next to two icon+label pairs,
    // and a bordered ProjectChip — four grammars in one row. Each property must now
    // carry a marker, and nothing may wear a chip/pill.
    resolvedIssue = {
      id: "issue-uuid",
      title: "Fix the login bug",
      status: "blocked",
      priority: "high",
      assignee_type: "member",
      assignee_id: "user-1",
      project_id: "proj-7",
    };
    resolvedProject = { id: "proj-7", title: "Doudizhu", icon: "🃏" };
    const { container } = render(
      <InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />,
    );

    // The assignee is an avatar + name, no longer bare text.
    expect(screen.getByTestId("assignee-avatar")).toHaveAttribute("data-actor", "user-1");
    expect(screen.getByText("Alice")).toBeInTheDocument();
    // The project is icon + title, NOT a chip.
    expect(screen.getByTestId("project-icon")).toBeInTheDocument();
    expect(container.querySelector(".project-chip")).toBeNull();
  });

  it("never nests a hover card or link inside the peek (#517)", () => {
    // The avatar already lives inside the peek's HoverCardContent — its own hover
    // card would stack a popover on a popover, and the peek is read-only.
    resolvedIssue = {
      id: "issue-uuid",
      title: "Fix the login bug",
      status: "todo",
      assignee_type: "agent",
      assignee_id: "agent-9",
    };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    const avatar = screen.getByTestId("assignee-avatar");
    expect(avatar).toHaveAttribute("data-hover-card", "false");
    expect(avatar).toHaveAttribute("data-profile-link", "false");
  });

  it("omits each property independently when absent — never a placeholder (#504)", () => {
    // Title + status only: no priority, unassigned, no project.
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "todo" };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByText("Todo")).toBeInTheDocument();
    expect(screen.queryByTestId("priority-icon")).toBeNull();
    expect(screen.queryByTestId("assignee-avatar")).toBeNull();
    expect(screen.queryByTestId("project-icon")).toBeNull();
  });

  it("shows a project the cached list has not got — degrades, never fakes (#517)", () => {
    // project_id is set but nothing resolves it: render nothing rather than a
    // guessed name or a lone icon.
    resolvedIssue = { id: "issue-uuid", title: "Fix the login bug", status: "todo", project_id: "proj-x" };
    resolvedProject = undefined;
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    expect(screen.getByRole("link", { name: "Fix the login bug" })).toBeInTheDocument();
    expect(within(screen.getByTestId("issue-hover-content")).getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.queryByTestId("project-icon")).toBeNull();
  });

  it("ignores an unknown priority instead of crashing the card (#504, Wren's catch)", () => {
    // `Issue.priority` is TYPED IssuePriority but arrives via an unvalidated API
    // cast: an unseen value would make PRIORITY_CONFIG[value] undefined and throw
    // on `.label`, taking the whole peek down. Mirror the status whitelist.
    resolvedIssue = {
      id: "issue-uuid",
      title: "Fix the login bug",
      status: "todo",
      priority: "catastrophic",
    };
    render(<InlineReferenceContent content="see #MUL-9 pls" parts={[issueRef(4, 10)]} />);

    // Card still renders; the unknown priority is simply dropped.
    expect(within(screen.getByTestId("issue-hover-content")).getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.getByText("Todo")).toBeInTheDocument();
    expect(screen.queryByTestId("priority-icon")).toBeNull();
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
    const peek = screen.getByTestId("issue-hover-content");
    expect(peek).toBeInTheDocument();
    expect(within(peek).getByText("Fix the login bug")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Fix the login bug" })).toBeInTheDocument();
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
