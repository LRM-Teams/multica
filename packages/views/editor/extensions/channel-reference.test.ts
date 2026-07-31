// @vitest-environment node
import { describe, it, expect } from "vitest";
import { ChannelReferenceExtension } from "./channel-reference";

const tokenizer = ChannelReferenceExtension.config.markdownTokenizer!;
const startFn = tokenizer.start as (src: string) => number;
const tokenizeFn = tokenizer.tokenize as (
  src: string,
) => { type: string; raw: string; attributes: Record<string, string> } | undefined;
const renderMarkdown = ChannelReferenceExtension.config.renderMarkdown as (
  node: { attrs: Record<string, string> },
) => string;

function tokenize(src: string) {
  const start = startFn(src);
  if (start === -1) return undefined;
  return tokenizeFn(src.slice(start));
}

describe("channel reference tokenizer", () => {
  it("parses channel reference markdown as a channelReference node", () => {
    const token = tokenize("[general](mention://channel/aaa-bbb)");

    expect(token).toBeDefined();
    expect(token!.type).toBe("channelReference");
    expect(token!.attributes).toEqual({
      id: "aaa-bbb",
      label: "general",
    });
  });

  it("finds channel references nested inside task list Markdown", () => {
    const token = tokenize("- [ ] [general](mention://channel/aaa-bbb)");

    expect(token).toBeDefined();
    expect(token!.attributes.id).toBe("aaa-bbb");
    expect(token!.attributes.label).toBe("general");
  });

  it("round-trips labels with brackets", () => {
    const md = renderMarkdown({
      attrs: { id: "aaa-bbb", label: "team[eng]" },
    });

    expect(md).toBe("[team\\[eng\\]](mention://channel/aaa-bbb)");
    expect(tokenize(md)?.attributes.label).toBe("team[eng]");
  });
});
