// @vitest-environment node
import { describe, expect, it } from "vitest";
import { preprocessMarkdown } from "./preprocess";

// #531/#542 — the chat composer keeps bare URLs as PLAIN TEXT in the input.
// The linkify step (preprocessLinks) is a READ-side transform; running it on
// editable content is what re-linkified a typed URL on every keystroke through
// the setContent round-trip. `preprocessMarkdown(md, { linkify: false })` lets
// the composer skip it while the read/display path keeps the default.
describe("preprocessMarkdown — linkify option (#531/#542)", () => {
  const url = "https://wire.com/w";

  it("default: a bare URL is linkified (read-side behavior unchanged)", () => {
    expect(preprocessMarkdown(`see ${url} tail`)).toBe(`see [${url}](${url}) tail`);
  });

  it("linkify:false: a bare URL stays plain text", () => {
    expect(preprocessMarkdown(`see ${url} tail`, { linkify: false })).toBe(
      `see ${url} tail`,
    );
  });

  it("linkify:false is idempotent for a plain URL — the composer round-trip cannot re-inject a link", () => {
    // The chat round-trip feeds getMarkdown() back as defaultValue; when the
    // editor holds a plain URL, preprocessMarkdown(..., {linkify:false}) must
    // return byte-identical output so content-editor's normalized-equal guard
    // (Guard 3) skips setContent and the URL is never re-parsed / linkified.
    const plain = `probe ${url}`;
    expect(preprocessMarkdown(plain, { linkify: false })).toBe(plain);
  });

  it("linkify:false still preserves an explicit markdown link (historical linked messages edit normally)", () => {
    const md = "see [docs](https://x.com/d)";
    expect(preprocessMarkdown(md, { linkify: false })).toBe(md);
  });
});
