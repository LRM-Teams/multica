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

vi.mock("./use-resolved-actor-identity", () => ({
  useResolvedActorIdentity: (actorId: string | undefined, mentionType: string | null) => {
    if (!actorId || !mentionType) return { displayName: null, avatarUrl: null };
    // LRM-515: directory miss (group manager) → member-profiles display_name
    if (actorId === "cb7e5c89-beckham") {
      return { displayName: "贝克汉姆", avatarUrl: null };
    }
    if (actorId === "agent-unresolved") {
      return { displayName: null, avatarUrl: null };
    }
    if (mentionType === "agent") return { displayName: "Bot", avatarUrl: null };
    return { displayName: "Alice", avatarUrl: null };
  },
}));

vi.mock("../issues/components/issue-mention-card", () => ({
  IssueMentionCard: ({
    issueId,
    fallbackLabel,
  }: {
    issueId: string;
    fallbackLabel?: string;
  }) => (
    <span data-testid="issue-mention-card" data-fallback-label={fallbackLabel ?? ""}>
      {fallbackLabel ?? issueId}
    </span>
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
const closeAgentPanelMock = vi.fn();
const openMemberPanelMock = vi.fn<(id: string) => void>();
vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (
    selector: (s: { open: (id: string) => void; close: () => void }) => unknown,
  ) => selector({ open: openAgentPanelMock, close: closeAgentPanelMock }),
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));
vi.mock("@multica/core/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/workspace")>();
  return {
    ...actual,
    useMemberPanelStore: (
      selector: (s: { open: (id: string) => void; close: () => void }) => unknown,
    ) => selector({ open: openMemberPanelMock, close: vi.fn() }),
  };
});
vi.mock("./agent-panel-context", () => ({
  useOpenAgentPanel: () => null,
}));
vi.mock("./member-panel-context", () => ({
  useOpenMemberPanel: () => null,
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

  it("forwards issue mention link text as fallbackLabel (LRM-493)", () => {
    // `[LRM-487](mention://issue/<uuid>)` must not drop the author label — that
    // is what made mobile paint a truncated bare UUID.
    render(
      <Markdown>
        {"[LRM-487](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047)"}
      </Markdown>,
    );

    const card = screen.getByTestId("issue-mention-card");
    expect(card).toHaveAttribute("data-fallback-label", "LRM-487");
    expect(card).toHaveTextContent("LRM-487");
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

  // LRM-515: authored label is the routing handle; chip primary ink is display_name.
  it("renders agent mention display_name instead of authored handle slug (LRM-515)", () => {
    render(
      <Markdown>
        {"清 [@bei-ke-han-mu-11](mention://agent/cb7e5c89-beckham)"}
      </Markdown>,
    );

    const trigger = screen.getByTestId("actor-profile-trigger");
    expect(trigger).toHaveTextContent("@贝克汉姆");
    expect(trigger).not.toHaveTextContent("bei-ke-han-mu-11");
    const chip = trigger.querySelector(".mention");
    expect(chip).toHaveAttribute("title", "@bei-ke-han-mu-11");
    expect(chip).not.toHaveAttribute("data-mention-unresolved");
  });

  it("grays unresolved agent mentions and keeps handle (never UUID) (LRM-515)", () => {
    render(
      <Markdown>
        {"Ping [@bei-ke-han-mu-11](mention://agent/agent-unresolved)"}
      </Markdown>,
    );

    const trigger = screen.getByTestId("actor-profile-trigger");
    expect(trigger).toHaveTextContent("@bei-ke-han-mu-11");
    expect(trigger).not.toHaveTextContent("agent-unresolved");
    expect(trigger.querySelector(".mention")).toHaveAttribute("data-mention-unresolved", "true");
  });

  // #349/#447 gap fix: a rendered (read-only message) @agent mention must open
  // the side panel on click, not only in the editor. Regression guard for the
  // "editor mention wired but rendered mention wasn't" miss.
  it("opens the agent panel when a rendered agent mention is clicked", () => {
    openAgentPanelMock.mockClear();
    openMemberPanelMock.mockClear();
    render(<Markdown>{"Ping [@Bot](mention://agent/agent-9)"}</Markdown>);
    fireEvent.click(screen.getByTestId("actor-profile-trigger"));
    expect(openAgentPanelMock).toHaveBeenCalledWith("agent-9");
    expect(openMemberPanelMock).not.toHaveBeenCalled();
  });

  // LRM-893: member @mention click opens the same Profile dock as avatar click
  // (not the empty hover peek alone).
  it("opens the member panel when a rendered member mention is clicked (LRM-893)", () => {
    openAgentPanelMock.mockClear();
    closeAgentPanelMock.mockClear();
    openMemberPanelMock.mockClear();
    render(<Markdown>{"Ping [@Alice](mention://member/user-1)"}</Markdown>);
    fireEvent.click(screen.getByTestId("actor-profile-trigger"));
    expect(closeAgentPanelMock).toHaveBeenCalled();
    expect(openMemberPanelMock).toHaveBeenCalledWith("user-1");
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

    // Slack soft-bg tokens — same fill for person/agent/@all; no per-id rainbow.
    for (const el of [member, agent, all]) {
      expect(el).toHaveClass("text-brand");
      expect(el).toHaveClass("font-bold");
      expect(el).toHaveClass("bg-brand/[0.10]");
      expect(el).toHaveClass("rounded-sm");
      expect(el).toHaveClass("px-0.5");
      expect(el).not.toHaveClass("rounded-full");
      expect(el).not.toHaveAttribute("style");
    }
  });

  it("marks a mention of the current viewer as self kind", () => {
    const { container } = render(
      <Markdown>{"Hey [@me](mention://member/user-1)"}</Markdown>,
    );

    const self = container.querySelector('[data-mention-type="member"]');
    expect(self).toHaveAttribute("data-mention-kind", "self");
    expect(self).toHaveClass("font-bold");
    expect(self).toHaveClass("bg-[#faf0c8]");
    expect(self).toHaveClass("text-foreground");
    expect(self).toHaveClass("dark:bg-brand/[0.14]");
    expect(self).toHaveClass("dark:text-brand");
    expect(self).not.toHaveClass("text-brand");
  });

  it("does not highlight inline code text", () => {
    const { container } = render(
      <Markdown highlightQuery="abc">{"abc stays visible, `abc` stays code"}</Markdown>,
    );

    expect(container.querySelectorAll("mark")).toHaveLength(1);
    expect(container.querySelector("code mark")).toBeNull();
    expect(container.querySelector("code")?.textContent).toBe("abc");
  });

  describe("inline mode", () => {
    it("formats inline emphasis (bold / italic / inline code)", () => {
      const { container } = render(
        <Markdown mode="inline">{"a **bold** _em_ and `code` here"}</Markdown>,
      );

      expect(container.querySelector("strong")?.textContent).toBe("bold");
      expect(container.querySelector("em")?.textContent).toBe("em");
      expect(container.querySelector("code")?.textContent).toBe("code");
    });

    it("degrades a truncated unclosed code fence to plain text, never a code block", () => {
      // A cut Output summary: the ```json fence is never closed. Block chrome
      // would swallow the tail into a broken code block (the v0 bug this mode
      // prevents) — inline mode renders the content as flat text instead.
      const { container } = render(
        <Markdown mode="inline">{'Looking at it:\n\n```json\n{"summary":"No new'}</Markdown>,
      );

      expect(container.querySelector("pre")).toBeNull();
      expect(container.textContent).toContain("Looking at it:");
      expect(container.textContent).toContain('{"summary":"No new');
    });

    it("flattens block structure (headings, lists) to inline text", () => {
      const { container } = render(
        <Markdown mode="inline">{"# Heading\n\n- one\n- two"}</Markdown>,
      );

      expect(container.querySelector("h1")).toBeNull();
      expect(container.querySelector("ul")).toBeNull();
      expect(container.querySelector("li")).toBeNull();
      expect(container.textContent).toContain("Heading");
      expect(container.textContent).toContain("one");
      expect(container.textContent).toContain("two");
    });
  });
});
