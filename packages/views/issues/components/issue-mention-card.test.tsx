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

  it("renders title as primary ink — never LRM-xxx as main text (LRM-508)", () => {
    resolvedIssue = {
      id: "issue-uuid",
      identifier: "LRM-126",
      title: "Fix the login bug",
      status: "todo",
    };
    const { container } = render(<IssueMentionCard issueId="LRM-126" fallbackLabel="LRM-126" />);

    const link = container.querySelector("a[data-ref-source]");
    expect(link).toHaveTextContent("Fix the login bug");
    expect(link).not.toHaveTextContent("LRM-126");
    expect(link).toHaveClass("text-brand");
    // Muted identifier is secondary only (LRM-423 parity).
    expect(container.textContent).toContain("LRM-126");
    expect(container.querySelector(".issue-chip")).toBeNull();
    expect(container.querySelector(".issue-mention")).toBeNull();
  });

  it("declares itself an issue ref so generic link affordances stand down", () => {
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

  it("paints title for mention://issue/<uuid> — never LRM-xxx or UUID (LRM-508)", () => {
    resolvedIssue = {
      id: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
      identifier: "LRM-487",
      title: "Soft-ask design",
      status: "todo",
    };
    const { container } = render(
      <IssueMentionCard
        issueId="fe57cec6-0a45-4d90-9ef6-6571f429c047"
        fallbackLabel="LRM-487"
      />,
    );
    const link = container.querySelector("a[data-ref-source]");
    expect(link).toHaveTextContent("Soft-ask design");
    expect(link).not.toHaveTextContent("LRM-487");
    expect(link).not.toHaveTextContent("fe57cec6");
  });

  it("renders nothing when resolved without a title — no silent LRM-xxx (LRM-508/238)", () => {
    resolvedIssue = {
      id: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
      identifier: "LRM-487",
      title: "   ",
      status: "todo",
    };
    const { container } = render(
      <IssueMentionCard
        issueId="fe57cec6-0a45-4d90-9ef6-6571f429c047"
        fallbackLabel="LRM-487"
      />,
    );
    expect(container.querySelector("a[data-ref-source]")).toBeNull();
    expect(container.textContent).toBe("");
  });

  it("renders nothing rather than a bare UUID while unresolved (LRM-508)", () => {
    resolvedIssue = undefined;
    const { container } = render(
      <IssueMentionCard issueId="fe57cec6-0a45-4d90-9ef6-6571f429c047" fallbackLabel="LRM-487" />,
    );
    expect(container.querySelector("a[data-ref-source]")).toBeNull();
    expect(container.textContent).toBe("");
  });
});
