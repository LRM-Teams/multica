import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Markdown } from "./markdown";

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (state: { cdnDomain: string }) => unknown) =>
    selector({ cdnDomain: "" }),
}));

vi.mock("@multica/core/api", () => ({
  // Web shape: empty base URL (the Next.js rewrite proxies /api/* same-origin),
  // so sticker srcs stay site-relative.
  api: { getBaseUrl: () => "" },
}));

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
  IssueMentionCard: ({ issueId }: { issueId: string }) => (
    <span data-testid="issue-mention-card">{issueId}</span>
  ),
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspaceSlug: () => "acme",
    useRequiredWorkspaceSlug: () => "acme",
    // Avoid the real hook's useQuery (no QueryClient in this unit test). Null
    // workspace → no issue-prefix → issue-ref auto-linking is a no-op here.
    useCurrentWorkspace: () => null,
    useWorkspacePaths: () => ({
      ...actual.paths.workspace("acme"),
      projectDetail: (projectId: string) => `/projects/${projectId}`,
    }),
  };
});

vi.mock("../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
  }: {
    href: string;
    children: ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));

vi.mock("../projects/components/project-chip", () => ({
  ProjectChip: ({ projectId }: { projectId: string }) => (
    <span data-testid="project-chip">{projectId}</span>
  ),
}));

vi.mock("./actor-profile-popover", () => ({
  ActorProfileTrigger: ({
    memberType,
    memberId,
    children,
    onClickCapture,
  }: {
    memberType: string;
    memberId: string;
    children: ReactNode;
    onClickCapture?: React.MouseEventHandler;
  }) => (
    <span
      data-testid="actor-profile-trigger"
      data-member-type={memberType}
      data-member-id={memberId}
      onClickCapture={onClickCapture}
    >
      {children}
    </span>
  ),
}));

const openAgentPanelMock = vi.fn<(id: string) => void>();
vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (selector: (s: { open: (id: string) => void }) => unknown) =>
    selector({ open: openAgentPanelMock }),
}));
vi.mock("./agent-panel-context", () => ({
  useOpenAgentPanel: () => null,
}));

const ligatureClasses = [
  "[font-variant-ligatures:none]",
  "[font-feature-settings:'liga'_0]",
];

describe("Markdown", () => {
  it("disables ligatures inside raw code tags", () => {
    render(<Markdown>{"<code>uv run --extra dev pytest -q</code>"}</Markdown>);

    expect(screen.getByText("uv run --extra dev pytest -q")).toHaveClass(...ligatureClasses);
  });

  it("disables ligatures inside fenced code blocks", () => {
    render(<Markdown>{"```sh\nuv run --extra dev pytest -q\n```"}</Markdown>);

    expect(screen.getByText("uv run --extra dev pytest -q")).toHaveClass(...ligatureClasses);
  });

  it("disables ligatures in terminal-mode code", () => {
    render(<Markdown mode="terminal">{"<code>uv run --extra dev pytest -q</code>"}</Markdown>);

    expect(screen.getByText("uv run --extra dev pytest -q")).toHaveClass(...ligatureClasses);
  });

  it("renders slash skill links as slash command pills", () => {
    const { container } = render(
      <Markdown>[/deploy](slash://skill/abc-123)</Markdown>,
    );

    const pill = container.querySelector(".slash-command");
    expect(pill).not.toBeNull();
    expect(pill?.textContent).toBe("/deploy");
  });

  it("renders project mention links as project chips", () => {
    render(<Markdown>{"[Roadmap](mention://project/project-123)"}</Markdown>);

    expect(screen.getByTestId("project-chip")).toHaveTextContent("project-123");
    expect(screen.getByRole("link")).toHaveAttribute("href", "/projects/project-123");
  });

  it("renders a :sticker:<id>: token as a sticker image", () => {
    const { container } = render(<Markdown>{"nice work :sticker:tada:"}</Markdown>);

    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    expect(img).toHaveAttribute("src", "/api/stickers/tada");
    expect(img).toHaveAttribute("alt", "sticker:tada");
    // Surrounding text is preserved (the token is a garnish, not a replacement).
    expect(container.textContent).toContain("nice work");
  });

  it("renders sticker tokens with hyphenated ids", () => {
    const { container } = render(<Markdown>{":sticker:thumbs-up:"}</Markdown>);

    const img = container.querySelector("img");
    expect(img).toHaveAttribute("src", "/api/stickers/thumbs-up");
  });

  it("can leave sticker shortcodes as literal text", () => {
    const { container } = render(
      <Markdown enableStickerShortcodes={false}>{"nice work :sticker:tada:"}</Markdown>,
    );

    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("nice work :sticker:tada:");
  });

  it("does not treat a non-sticker word with colons as a sticker", () => {
    const { container } = render(<Markdown>{"ratio is 3:4:5 here"}</Markdown>);

    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("3:4:5");
  });

  it("highlights every visible search phrase match case-insensitively", () => {
    const { container } = render(
      <Markdown highlightQuery="abc">{"abc and ABC and abcd"}</Markdown>,
    );

    const marks = Array.from(container.querySelectorAll("mark"));
    expect(marks.map((mark) => mark.textContent)).toEqual(["abc", "ABC", "abc"]);
    for (const mark of marks) {
      expect(mark).toHaveClass(
        "bg-primary/20",
        "text-foreground",
        "rounded-[3px]",
        "px-0.5",
        "box-decoration-clone",
      );
    }
  });

  it("keeps highlighted link text clickable", () => {
    const { container } = render(
      <Markdown highlightQuery="docs">{"Read [Docs](https://example.com/docs)"}</Markdown>,
    );

    const link = screen.getByRole("link", { name: "Docs" });
    expect(link).toHaveAttribute("href", "https://example.com/docs");
    expect(container.querySelector("a mark")?.textContent).toBe("Docs");
  });

  it("highlights member mention text without breaking the mention chip", () => {
    const { container } = render(
      <Markdown highlightQuery="ali">{"Ping [@Alice](mention://member/user-1)"}</Markdown>,
    );

    expect(container.textContent).toContain("@Alice");
    expect(container.querySelector("mark")?.textContent).toBe("Ali");
  });

  it("wraps member mentions in the full profile popover trigger", () => {
    render(<Markdown>{"Ping [@Alice](mention://member/user-1)"}</Markdown>);

    const trigger = screen.getByTestId("actor-profile-trigger");
    expect(trigger).toHaveAttribute("data-member-type", "user");
    expect(trigger).toHaveAttribute("data-member-id", "user-1");
    expect(trigger.textContent).toContain("@Alice");
  });

  it("wraps agent mentions in the full profile popover trigger", () => {
    render(<Markdown>{"Ping [@Bot](mention://agent/agent-9)"}</Markdown>);

    const trigger = screen.getByTestId("actor-profile-trigger");
    expect(trigger).toHaveAttribute("data-member-type", "agent");
    expect(trigger).toHaveAttribute("data-member-id", "agent-9");
  });

  // #349/#447 gap fix: a rendered (read-only message) @agent mention must open
  // the side panel on click, not only in the editor. Regression guard for the
  // "editor mention wired but rendered mention wasn't" miss.
  it("opens the agent panel when a rendered agent mention is clicked", () => {
    openAgentPanelMock.mockClear();
    render(<Markdown>{"Ping [@Bot](mention://agent/agent-9)"}</Markdown>);
    fireEvent.click(screen.getByTestId("actor-profile-trigger"));
    expect(openAgentPanelMock).toHaveBeenCalledWith("agent-9");
  });

  it("does not open the panel for a rendered human member mention (v1: agents only)", () => {
    openAgentPanelMock.mockClear();
    render(<Markdown>{"Ping [@Alice](mention://member/user-1)"}</Markdown>);
    fireEvent.click(screen.getByTestId("actor-profile-trigger"));
    expect(openAgentPanelMock).not.toHaveBeenCalled();
  });

  it("renders @all as a styled pill without a profile hover card", () => {
    const { container } = render(
      <Markdown>{"Ping [@all](mention://all/all)"}</Markdown>,
    );

    expect(container.textContent).toContain("@all");
    expect(screen.queryByTestId("actor-profile-trigger")).toBeNull();
  });

  it("renders member/agent/@all mentions as brand semantic tokens, not identity colors", () => {
    const { container } = render(
      <Markdown>
        {
          "Ping [@Alice](mention://member/user-2) [@Bot](mention://agent/agent-1) [@all](mention://all/all)"
        }
      </Markdown>,
    );

    const tokens = container.querySelectorAll("[data-mention-kind]");
    expect(tokens.length).toBeGreaterThanOrEqual(3);

    const member = container.querySelector('[data-mention-type="member"]');
    const agent = container.querySelector('[data-mention-type="agent"]');
    const all = container.querySelector('[data-mention-type="all"]');

    expect(member).toHaveAttribute("data-mention-kind", "default");
    expect(agent).toHaveAttribute("data-mention-kind", "default");
    expect(all).toHaveAttribute("data-mention-kind", "all");

    // Brand-ink prose — no per-id inline style rainbow, no chip fill/padding.
    for (const el of [member, agent, all]) {
      expect(el).toHaveClass("text-brand");
      expect(el).not.toHaveAttribute("style");
    }
    expect(member).toHaveClass("font-medium");
    expect(all).toHaveClass("font-semibold");
  });

  it("marks a mention of the current viewer as self kind", () => {
    const { container } = render(
      <Markdown>{"Hey [@me](mention://member/user-1)"}</Markdown>,
    );

    const self = container.querySelector('[data-mention-type="member"]');
    expect(self).toHaveAttribute("data-mention-kind", "self");
    expect(self).toHaveClass("font-semibold");
  });

  it("does not highlight inline code text", () => {
    const { container } = render(
      <Markdown highlightQuery="abc">{"abc stays visible, `abc` stays code"}</Markdown>,
    );

    expect(container.querySelectorAll("mark")).toHaveLength(1);
    expect(container.querySelector("code mark")).toBeNull();
    expect(container.querySelector("code")?.textContent).toBe("abc");
  });
});
