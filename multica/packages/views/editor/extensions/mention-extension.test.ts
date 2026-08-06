// @vitest-environment node
import { describe, it, expect } from "vitest";
import { BaseMentionExtension } from "./mention-extension";

const tokenizer = BaseMentionExtension.config.markdownTokenizer!;

// The tiptap MarkdownTokenizer/renderMarkdown types have broad signatures
// (multi-arg overloads). Our extension always provides single-argument
// implementations, so cast for test convenience.
const startFn = tokenizer.start as (src: string) => number;
const tokenizeFn = tokenizer.tokenize as (
  src: string,
) => { type: string; raw: string; attributes: Record<string, string> } | undefined;
const renderMarkdown = BaseMentionExtension.config.renderMarkdown as (
  node: { attrs: Record<string, string | null | undefined> },
) => string;

function tokenize(src: string) {
  const start = startFn(src);
  if (start === -1) return undefined;
  return tokenizeFn(src.slice(start));
}

// The tokenizer parses the LEGACY `mention://` syntax so the editor can still
// load / edit historical messages stored before the bare-`@handle` cutover
// (#600). New content is serialized bare by renderMarkdown (see below).
describe("mention tokenizer (legacy mention:// reads)", () => {
  it("parses a plain mention", () => {
    const token = tokenize("[@Alice](mention://member/aaa-bbb)");
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("Alice");
    expect(token!.attributes.type).toBe("member");
    expect(token!.attributes.id).toBe("aaa-bbb");
  });

  it("parses a legacy mention with escaped brackets", () => {
    const token = tokenize("[@David\\[TF\\]](mention://agent/aaa-bbb)");
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("David[TF]");
    expect(token!.attributes.type).toBe("agent");
  });

  it("does not match an ordinary Markdown link before a mention", () => {
    const src =
      "Check [docs](https://example.com) - [@User](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)";

    // start() must NOT land on the [docs] link at index 6
    const start = startFn(src);
    expect(start).toBeGreaterThan(6);

    // tokenize from the correct start position
    const token = tokenizeFn(src.slice(start));
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("User");
    expect(token!.attributes.id).toBe("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
  });

  it("handles multiple ordinary links before a mention", () => {
    const src =
      "See [a](https://a.com) and [b](https://b.com) - [@Bot](mention://agent/abc-123)";
    const start = startFn(src);
    const token = tokenizeFn(src.slice(start));
    expect(token).toBeDefined();
    expect(token!.attributes.label).toBe("Bot");
  });

  it("does not parse issue references as actor mentions", () => {
    const token = tokenize("[MUL-123](mention://issue/aaa-bbb)");
    expect(token).toBeUndefined();
  });

  it("does not parse issue references nested inside task list Markdown", () => {
    const token = tokenize("- [ ] [MUL-123](mention://issue/aaa-bbb)");
    expect(token).toBeUndefined();
  });
});

// #600: the server hard-rejects the legacy `mention://` actor syntax. Actor
// mentions serialize as bare `@handle` plain text, which the server parses into
// a structured reference anchored to a UTF-16 span.
describe("mention renderMarkdown (bare @handle cutover)", () => {
  it("serializes a member mention as its bare @handle, not mention://", () => {
    expect(
      renderMarkdown({
        attrs: { id: "aaa-bbb", label: "Alice Wong", handle: "alice", type: "member" },
      }),
    ).toBe("@alice");
  });

  it("serializes an agent mention as its bare @handle", () => {
    expect(
      renderMarkdown({
        attrs: { id: "x-y-z", label: "David Thompson", handle: "david", type: "agent" },
      }),
    ).toBe("@david");
  });

  it("never emits mention:// or bracket-wrapping for an actor", () => {
    const md = renderMarkdown({
      attrs: { id: "aaa-bbb", label: "David[TF]", handle: "david-tf", type: "agent" },
    });
    expect(md).toBe("@david-tf");
    expect(md).not.toContain("mention://");
    expect(md).not.toContain("[");
  });

  it("falls back to the label for a legacy node with no handle", () => {
    // Nodes parsed from old `mention://` content carry a label but no handle;
    // they degrade to `@label` rather than re-emitting the rejected syntax.
    expect(
      renderMarkdown({ attrs: { id: "aaa-bbb", label: "alice", type: "member" } }),
    ).toBe("@alice");
  });

  it("serializes @all as the literal word (no broadcast, no ref)", () => {
    // The broadcast token was dropped in the cutover: the server neither parses
    // nor triggers @all, so it is plain text, never `mention://all/all`.
    expect(
      renderMarkdown({ attrs: { id: "all", label: "all", type: "all" } }),
    ).toBe("@all");
  });

  it("keeps non-actor references (project) on the legacy link form", () => {
    // Projects/issues are out of the #600 channel-actor cutover.
    expect(
      renderMarkdown({ attrs: { id: "proj-1", label: "Roadmap", type: "project" } }),
    ).toBe("[Roadmap](mention://project/proj-1)");
  });
});
