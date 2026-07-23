import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { IssueMentionCard } from "./issue-mention-card";

let resolvedIssue:
  | { id: string; identifier: string; title: string; status?: string; priority?: string }
  | undefined;

vi.mock("./issue-chip", () => ({
  useResolvedIssue: () => resolvedIssue,
  isIssueUuid: (v: string) => /^[0-9a-f-]{36}$/i.test(v),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/issues/${id}` }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Alice" }),
}));

vi.mock("@tanstack/react-query", () => ({ useQuery: () => ({ data: undefined }) }));

vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"] }),
  projectDetailOptions: () => ({ queryKey: ["project"] }),
}));

// Mirror the real AppLink: it extends AnchorHTMLAttributes and spreads {...props}.
vi.mock("../../navigation/app-link", () => ({
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

vi.mock("@multica/ui/components/ui/hover-card", () => ({
  HoverCard: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  HoverCardTrigger: ({ children }: { children: ReactNode }) => <span>{children}</span>,
  HoverCardContent: ({ children }: { children: ReactNode }) => (
    <div data-testid="issue-hover-content">{children}</div>
  ),
}));

vi.mock("./status-icon", () => ({ StatusIcon: () => <svg data-testid="status-icon" /> }));
vi.mock("./priority-icon", () => ({ PriorityIcon: () => <svg data-testid="priority-icon" /> }));
vi.mock("../../projects/components/project-icon", () => ({
  ProjectIcon: () => <span data-testid="project-icon" />,
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="assignee-avatar" />,
}));

describe("IssueMentionCard (#520 — the unanchored fallback)", () => {
  beforeEach(() => {
    resolvedIssue = undefined;
  });

  it("renders the SAME zero-decoration link as an anchored reference — never a chip", () => {
    // Frank caught one message rendering LRM-126 three times in two looks: two
    // anchored (plain link) and one unanchored (a bordered chip). A compat path may
    // survive; it may not grow a second face.
    resolvedIssue = {
      id: "issue-uuid",
      identifier: "LRM-126",
      title: "Fix the login bug",
      status: "todo",
    };
    const { container } = render(<IssueMentionCard issueId="LRM-126" fallbackLabel="LRM-126" />);

    // LRM-508: resolved main-line ink is the live title (identifier stays in peek).
    const link = container.querySelector("a[data-ref-source]");
    expect(link).toHaveTextContent("Fix the login bug");
    expect(link).not.toHaveTextContent("LRM-126");
    expect(link).toHaveClass("text-brand");
    // The chip's own class must not appear anywhere in a message body.
    expect(container.querySelector(".issue-chip")).toBeNull();
    // NOTE: `.issue-mention` is deliberately absent here — inside `.rich-text-editor`
    // that class forces `color: inherit; text-decoration: none`, i.e. the very
    // decoration #520 removed. Its OTHER job (suppressing the generic URL hover) is
    // carried by `data-issue-ref` below. Dropping the class without noticing it did
    // two jobs is what shipped Frank's double-hover bug.
    expect(container.querySelector(".issue-mention")).toBeNull();
  });

  it("declares itself an issue ref so generic link affordances stand down", () => {
    // Frank: hovering LRM-127 popped the peek AND a URL preview. The suppression is
    // an attribute — it says what the link IS, so a restyle cannot silently take the
    // behaviour with it (see link-hover-card.test.tsx for the other half).
    resolvedIssue = {
      id: "issue-uuid",
      identifier: "LRM-126",
      title: "Fix the login bug",
      status: "todo",
    };
    const { container } = render(<IssueMentionCard issueId="LRM-126" fallbackLabel="LRM-126" />);

    expect(container.querySelector("a[data-issue-ref]")).not.toBeNull();
  });

  it("carries the peek card, so the fallback is indistinguishable to a reader (#520)", () => {
    // Parker's ruling: don't use a degraded UX as a bug detector. The fallback holds
    // only an identifier, but useResolvedIssue accepts identifiers — which is exactly
    // how the old chip showed a title for mention://issue/LRM-126.
    resolvedIssue = {
      id: "issue-uuid",
      identifier: "LRM-126",
      title: "Fix the login bug",
      status: "todo",
    };
    render(<IssueMentionCard issueId="LRM-126" fallbackLabel="LRM-126" />);

    expect(screen.getByTestId("issue-hover-content")).toHaveTextContent("Fix the login bug");
  });

  it("marks itself provenance `fallback` — the only remaining tell of a missed anchor", () => {
    // Invisible to readers, visible to assertions. Iris's acceptance: N occurrences
    // of one identifier in a message must ALL be `anchor`; a `fallback` = the parser
    // skipped one (#521). Scaffolding — deleted with this path once #521 lands.
    resolvedIssue = {
      id: "issue-uuid",
      identifier: "LRM-126",
      title: "Fix the login bug",
      status: "todo",
    };
    const { container } = render(<IssueMentionCard issueId="LRM-126" fallbackLabel="LRM-126" />);

    expect(container.querySelector("a[data-ref-source]")).toHaveAttribute(
      "data-ref-source",
      "fallback",
    );
  });

  it("keeps an unresolvable auto-linked identifier as plain text — no dead link", () => {
    resolvedIssue = undefined;
    render(<IssueMentionCard issueId="LRM-999" fallbackLabel="LRM-999" />);

    const text = screen.getByText("LRM-999");
    expect(text.tagName).toBe("SPAN");
    expect(text).not.toHaveAttribute("href");
  });

  it("paints the author link label for mention://issue/<uuid> — never the UUID (LRM-493)", () => {
    // Morgan: `[LRM-487](mention://issue/fe57cec6-…)` must read LRM-487 on mobile,
    // not a truncated UUID. Label comes from markdown link text.
    resolvedIssue = undefined;
    const { container } = render(
      <IssueMentionCard
        issueId="fe57cec6-0a45-4d90-9ef6-6571f429c047"
        fallbackLabel="LRM-487"
      />,
    );
    const link = container.querySelector("a[data-ref-source]");
    expect(link).toHaveTextContent("LRM-487");
    expect(link).not.toHaveTextContent("fe57cec6");
  });

  it("prefers live title over a UUID-shaped label once resolved (LRM-508)", () => {
    resolvedIssue = {
      id: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
      identifier: "LRM-487",
      title: "Soft-ask design",
      status: "todo",
    };
    const { container } = render(
      <IssueMentionCard
        issueId="fe57cec6-0a45-4d90-9ef6-6571f429c047"
        fallbackLabel="fe57cec6-0a45-4d90-9ef6-6571f429c047"
      />,
    );
    const link = container.querySelector("a[data-ref-source]");
    expect(link).toHaveTextContent("Soft-ask design");
    expect(link).not.toHaveTextContent("LRM-487");
    expect(link).not.toHaveTextContent("fe57cec6");
  });

  it("renders nothing rather than a bare UUID while unresolved (LRM-493)", () => {
    resolvedIssue = undefined;
    const { container } = render(
      <IssueMentionCard issueId="fe57cec6-0a45-4d90-9ef6-6571f429c047" />,
    );
    expect(container.querySelector("a[data-ref-source]")).toBeNull();
    expect(container.textContent).toBe("");
  });
});
