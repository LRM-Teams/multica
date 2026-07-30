// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  mentionTokenClassName,
  messageCollapseFadeClassName,
  resolveMessageCollapseFadeVariant,
  resolveMentionTokenKind,
  SELF_MENTION_ROW_CLASS,
  SELF_MENTION_ROW_MENTION_CLASS,
} from "./mention-token";

describe("resolveMentionTokenKind", () => {
  it("maps @all to the all kind", () => {
    expect(resolveMentionTokenKind("all", "all", "user-1")).toBe("all");
  });

  it("maps a member mention of the viewer to self", () => {
    expect(resolveMentionTokenKind("member", "user-1", "user-1")).toBe("self");
  });

  it("keeps other members, agents, and squads on default", () => {
    expect(resolveMentionTokenKind("member", "user-2", "user-1")).toBe("default");
    expect(resolveMentionTokenKind("agent", "agent-1", "user-1")).toBe("default");
    expect(resolveMentionTokenKind("squad", "squad-1", "user-1")).toBe("default");
  });

  it("does not treat agent ids as self even when they match the viewer id", () => {
    expect(resolveMentionTokenKind("agent", "user-1", "user-1")).toBe("default");
  });
});

describe("mentionTokenClassName", () => {
  it("uses brand ink + soft rest fill (Slack token, not bare prose)", () => {
    const cls = mentionTokenClassName("default");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("font-bold");
    expect(cls).toContain("mention");
    expect(cls).toContain("bg-brand/[0.10]");
    expect(cls).toContain("rounded-sm");
    expect(cls).toContain("px-0.5");
    expect(cls).not.toContain("rounded-full");
  });

  it("keeps @all on the same soft brand token as people/agents/squads", () => {
    const cls = mentionTokenClassName("all");
    expect(cls).toContain("text-brand");
    expect(cls).toContain("font-bold");
    expect(cls).toContain("bg-brand/[0.10]");
    expect(cls).not.toContain("rounded-full");
    expect(cls).not.toContain("bg-[#faf0c8]");
  });

  it("uses warm yellow fill + ink text for @self in light (row wash is separate)", () => {
    const cls = mentionTokenClassName("self");
    expect(cls).toContain("bg-[#faf0c8]");
    expect(cls).toContain("text-foreground");
    expect(cls).toContain("font-bold");
    expect(cls).not.toContain("rounded-full");
  });

  it("uses brand tint + brand ink for @self in dark (no cream yellow)", () => {
    const cls = mentionTokenClassName("self");
    expect(cls).toContain("dark:bg-brand/[0.14]");
    expect(cls).toContain("dark:text-brand");
    expect(cls).toContain("dark:hover:bg-brand/[0.18]");
    expect(cls).toContain("dark:focus-visible:bg-brand/[0.18]");
    // Dark path must not keep the light cream wash under dark:
    expect(cls).not.toContain("dark:bg-[#faf0c8]");
    expect(cls).not.toContain("dark:text-foreground");
  });
});

describe("SELF_MENTION_ROW_CLASS", () => {
  it("keeps warm wash in light and uses brand bar + cool tint in dark", () => {
    expect(SELF_MENTION_ROW_CLASS).toContain("bg-[#fef9e8]");
    expect(SELF_MENTION_ROW_CLASS).toContain("dark:border-l-2");
    expect(SELF_MENTION_ROW_CLASS).toContain("dark:border-brand");
    expect(SELF_MENTION_ROW_CLASS).toContain("dark:bg-brand/[0.06]");
  });
});

describe("SELF_MENTION_ROW_MENTION_CLASS", () => {
  it("strips dark mention pill fill on self-mentioned rows", () => {
    expect(SELF_MENTION_ROW_MENTION_CLASS).toContain(
      "[&_.mention]:dark:bg-transparent",
    );
    expect(SELF_MENTION_ROW_MENTION_CLASS).toContain(
      "[&_.mention]:dark:hover:bg-brand/[0.08]",
    );
  });
});

describe("messageCollapseFadeClassName", () => {
  it("uses row-background tokens per variant (LRM-368)", () => {
    expect(messageCollapseFadeClassName("default")).toContain("from-background");
    expect(messageCollapseFadeClassName("self-mention")).toContain(
      "from-[#fef9e8]",
    );
    expect(messageCollapseFadeClassName("self-mention")).toContain(
      "dark:from-brand/[0.06]",
    );
    expect(messageCollapseFadeClassName("highlighted")).toContain(
      "from-primary/10",
    );
    expect(messageCollapseFadeClassName("search")).toContain("from-primary/5");
  });

  it("prioritizes search, then highlight, then self-mention", () => {
    expect(
      resolveMessageCollapseFadeVariant({
        selfMentioned: true,
        highlighted: true,
        searchHighlighted: true,
      }),
    ).toBe("search");
    expect(
      resolveMessageCollapseFadeVariant({
        selfMentioned: true,
        highlighted: true,
        searchHighlighted: false,
      }),
    ).toBe("highlighted");
    expect(
      resolveMessageCollapseFadeVariant({
        selfMentioned: true,
        highlighted: false,
        searchHighlighted: false,
      }),
    ).toBe("self-mention");
  });
});
