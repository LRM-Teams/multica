// @vitest-environment node
import { describe, it, expect } from "vitest";
import { IssueReferenceExtension } from "./issue-reference";

const tokenizer = IssueReferenceExtension.config.markdownTokenizer!;
const startFn = tokenizer.start as (src: string) => number;
const tokenizeFn = tokenizer.tokenize as (
  src: string,
) => { type: string; raw: string; attributes: Record<string, string> } | undefined;
const renderMarkdown = IssueReferenceExtension.config.renderMarkdown as (
  node: { attrs: Record<string, string> },
) => string;

function tokenize(src: string) {
  const start = startFn(src);
  if (start === -1) return undefined;
  return tokenizeFn(src.slice(start));
}

describe("issue reference tokenizer", () => {
  it("parses legacy issue mention markdown as an issueReference node", () => {
    const token = tokenize("[LRM-36](mention://issue/aaa-bbb)");

    expect(token).toBeDefined();
    expect(token!.type).toBe("issueReference");
    expect(token!.attributes).toEqual({
      id: "aaa-bbb",
      label: "LRM-36",
    });
  });

  it("finds issue references nested inside task list Markdown", () => {
    const token = tokenize("- [ ] [LRM-36](mention://issue/aaa-bbb)");

    expect(token).toBeDefined();
    expect(token!.attributes.id).toBe("aaa-bbb");
    expect(token!.attributes.label).toBe("LRM-36");
  });

  it("round-trips labels with brackets", () => {
    const md = renderMarkdown({
      attrs: { id: "aaa-bbb", label: "LRM[36]" },
    });

    expect(md).toBe("[LRM\\[36\\]](mention://issue/aaa-bbb)");
    expect(tokenize(md)?.attributes.label).toBe("LRM[36]");
  });
});
