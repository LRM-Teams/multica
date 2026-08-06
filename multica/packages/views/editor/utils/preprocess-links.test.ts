// @vitest-environment node
import { describe, expect, it } from "vitest";
import { preprocessLinks, detectLinks } from "@multica/ui/markdown/linkify";

// The bug: linkify-it does not treat CJK full-width punctuation as a URL
// boundary, so the href can swallow trailing punctuation and the Chinese
// characters that follow it (up to the next space). The fix truncates the
// detected URL at the first CJK full-width punctuation character.

describe("preprocessLinks — CJK punctuation boundary", () => {
  it("stops URL at ideographic full stop 。", () => {
    const out = preprocessLinks("见 https://example.com/path。然后继续");
    expect(out).toBe("见 [https://example.com/path](https://example.com/path)。然后继续");
  });

  it("stops URL at fullwidth comma ，", () => {
    const out = preprocessLinks("打开 https://example.com/a，以及其他");
    expect(out).toBe("打开 [https://example.com/a](https://example.com/a)，以及其他");
  });

  it("stops URL at ideographic comma 、", () => {
    const out = preprocessLinks("两个地址 https://a.com/x、https://b.com/y");
    expect(out).toBe(
      "两个地址 [https://a.com/x](https://a.com/x)、[https://b.com/y](https://b.com/y)",
    );
  });

  it("stops URL at fullwidth right paren ）", () => {
    const out = preprocessLinks("（见 https://example.com/x）后文");
    expect(out).toBe("（见 [https://example.com/x](https://example.com/x)）后文");
  });

  it("stops URL at corner bracket 」", () => {
    const out = preprocessLinks("「https://example.com/a」后文");
    expect(out).toBe("「[https://example.com/a](https://example.com/a)」后文");
  });

  it("stops URL at fullwidth exclamation ！", () => {
    const out = preprocessLinks("太好了 https://example.com/x！继续");
    expect(out).toBe("太好了 [https://example.com/x](https://example.com/x)！继续");
  });

  it("handles the original bug report (PR link then 。 then more text)", () => {
    const out = preprocessLinks(
      "已合并 PR #1623：https://github.com/multica-ai/multica/pull/1623。merge commit",
    );
    expect(out).toBe(
      "已合并 PR #1623：[https://github.com/multica-ai/multica/pull/1623](https://github.com/multica-ai/multica/pull/1623)。merge commit",
    );
  });

  it("does not swallow the entire remainder when there is no trailing space", () => {
    const out = preprocessLinks("https://github.com/x/y/issues/1619。我接下来把这个");
    expect(out).toBe(
      "[https://github.com/x/y/issues/1619](https://github.com/x/y/issues/1619)。我接下来把这个",
    );
  });

  it("preserves ASCII trailing period handling (no regression)", () => {
    const out = preprocessLinks("visit https://example.com/path. next.");
    expect(out).toBe("visit [https://example.com/path](https://example.com/path). next.");
  });

  it("preserves plain URL with no trailing punctuation (no regression)", () => {
    const out = preprocessLinks("go https://example.com/path");
    expect(out).toBe("go [https://example.com/path](https://example.com/path)");
  });

  it("preserves CJK letters inside URL path (only trims on punctuation)", () => {
    const out = preprocessLinks("https://zh.wikipedia.org/wiki/中国 参考");
    expect(out).toBe(
      "[https://zh.wikipedia.org/wiki/中国](https://zh.wikipedia.org/wiki/中国) 参考",
    );
  });

  it("does not re-link an already-linked URL that contains 。", () => {
    // If a user or upstream already wrote [text](url。), we leave it alone.
    const input = "见 [link](https://example.com/x。)后文";
    expect(preprocessLinks(input)).toBe(input);
  });

  it("does not linkify fuzzy domains inside existing markdown link labels", () => {
    const input =
      "数据来源：[NBA.com Schedule](https://www.nba.com/schedule)、[NBC Insider](https://www.nbc.com/nbc-insider/every-nba-playoff-game-this-week-on-nbc-peacock-april-25-28)";

    expect(preprocessLinks(input)).toBe(input);
  });

  it("still linkifies fuzzy domains outside existing markdown links", () => {
    const input = "数据来源：[NBA.com Schedule](https://www.nba.com/schedule)，官网 NBA.com";

    expect(preprocessLinks(input)).toBe(
      "数据来源：[NBA.com Schedule](https://www.nba.com/schedule)，官网 [NBA.com](http://NBA.com)",
    );
  });
});

// task #537 — CJK *letters* (not punctuation) glued onto a host. linkify-it
// treats CJK letters as valid domain-label chars (needed for IDN), so a URL
// typed with no separator before a Chinese word (`https://x.com吗`) swallows the
// word into the host. The five signed output contracts + two documented
// consequences below define the boundary; the fix uses linkify-it's own fuzzy
// matcher as the host-validity oracle (no TLD table), so real IDN hosts stay
// whole. This is a product heuristic, not IRI/IDNA truth.
describe("preprocessLinks — CJK letter host overrun (#537)", () => {
  // Contract 1 (the bug): trailing CJK after a complete host is excluded.
  it("contract 1: excludes trailing CJK glued onto host (吗 outside link)", () => {
    expect(preprocessLinks("参见 https://x.com吗")).toBe(
      "参见 [https://x.com](https://x.com)吗",
    );
  });

  // Contract 5 + IDN: CJK that forms a real IDN label stays inside the link.
  it("contract 5: preserves IDN host with CJK label 中国.cn", () => {
    expect(preprocessLinks("看 https://中国.cn/x 参考")).toBe(
      "看 [https://中国.cn/x](https://中国.cn/x) 参考",
    );
  });

  it("preserves numeric+CJK IDN labels 123中国.cn / 中国123.cn", () => {
    expect(preprocessLinks("a https://123中国.cn b")).toBe(
      "a [https://123中国.cn](https://123中国.cn) b",
    );
    expect(preprocessLinks("a https://中国123.cn b")).toBe(
      "a [https://中国123.cn](https://中国123.cn) b",
    );
  });

  // Reverse contract: don't degrade into "all CJK terminates the URL".
  it("contract 3: keeps CJK in the path (x.com/吗 stays whole)", () => {
    expect(preprocessLinks("a https://x.com/吗 b")).toBe(
      "a [https://x.com/吗](https://x.com/吗) b",
    );
  });

  it("contract 4: keeps CJK in the query (x.com?q=中 stays whole)", () => {
    expect(preprocessLinks("a https://x.com?q=中 b")).toBe(
      "a [https://x.com?q=中](https://x.com?q=中) b",
    );
  });

  // Only the trailing glued run is stripped; a real IDN label earlier stays.
  it("strips only the glued tail on 中国.cn吗 (keeps 中国.cn)", () => {
    expect(preprocessLinks("a https://中国.cn吗 b")).toBe(
      "a [https://中国.cn](https://中国.cn)吗 b",
    );
  });

  it("preserves a mixed ASCII+CJK non-trailing label abc中.cn (no regression)", () => {
    expect(preprocessLinks("a https://abc中.cn b")).toBe(
      "a [https://abc中.cn](https://abc中.cn) b",
    );
  });

  it("handles userinfo in the authority before the glued CJK", () => {
    expect(preprocessLinks("a https://user@x.com吗 b")).toBe(
      "a [https://user@x.com](https://user@x.com)吗 b",
    );
  });

  it("truncates when the URL is at index 0", () => {
    expect(preprocessLinks("https://x.com吗 后文")).toBe(
      "[https://x.com](https://x.com)吗 后文",
    );
  });

  it("two URLs in one text: first truncated, second still detected", () => {
    expect(preprocessLinks("看 https://a.com吗 和 https://b.com/c 结束")).toBe(
      "看 [https://a.com](https://a.com)吗 和 [https://b.com/c](https://b.com/c) 结束",
    );
  });

  // Boundary control (a requirement, and it bounds documented cost 2 below):
  // the fix is NOT "ASCII-only". IDN TLDs the fuzzy oracle DOES recognize
  // (Cyrillic .рф, punycode roots) truncate the glued Han correctly.
  it("recognized IDN TLD .рф + Han (x.рф吗) truncates correctly", () => {
    expect(preprocessLinks("a https://x.рф吗 b")).toBe(
      "a [https://x.рф](https://x.рф)吗 b",
    );
  });

  it("punycode TLD + Han (x.xn--p1ai吗) truncates correctly", () => {
    expect(preprocessLinks("a https://x.xn--p1ai吗 b")).toBe(
      "a [https://x.xn--p1ai](https://x.xn--p1ai)吗 b",
    );
  });

  // The punctuation boundary still fires alongside the letter rule.
  it("still truncates at CJK punctuation after the host (no regression)", () => {
    expect(preprocessLinks("见 https://x.com。后文")).toBe(
      "见 [https://x.com](https://x.com)。后文",
    );
  });

  it("detectLinks reports the truncated span for contract 1", () => {
    const links = detectLinks("参见 https://x.com吗");
    expect(links).toHaveLength(1);
    expect(links[0]).toMatchObject({
      type: "url",
      text: "https://x.com",
      url: "https://x.com",
      start: 3,
      end: 16,
    });
  });
});

// ⚠️ These are NOT contracts. They pin KNOWN LIMITATIONS of the heuristic so a
// change to them is a visible, deliberate act (not a silent regression). When a
// limitation below is genuinely fixed, FLIP the expectation here — do not read
// these as "the required behaviour". (This separation exists because a
// casually-written `expect(...).toBe(...)` in three months reads as a spec.)
describe("preprocessLinks — CJK host overrun (#537) DOCUMENTED COSTS (flip when fixed)", () => {
  // Cost 1 (product heuristic, not IRI truth): an invalid host + CJK — never a
  // real host — is left untouched, so the trailing CJK stays in the link.
  it("cost 1: invalid host + CJK (x.zzz吗) is left untouched", () => {
    expect(preprocessLinks("a https://x.zzz吗 b")).toBe(
      "a [https://x.zzz吗](https://x.zzz吗) b",
    );
  });

  // Cost 2: the fuzzy oracle does not recognize the raw-Unicode IDN TLDs tested
  // (中国 公司 网络 みんな 한국), so Han glued after one is not stripped. NOT all
  // IDN — see the .рф / punycode contracts above, which DO truncate. Not a
  // regression; fixing needs a self-maintained IDN-TLD table (the "unbounded
  // list" this design avoids).
  it("cost 2: raw-Unicode IDN TLD + Han (x.中国吗) is left untouched", () => {
    expect(preprocessLinks("a https://x.中国吗 b")).toBe(
      "a [https://x.中国吗](https://x.中国吗) b",
    );
  });

  // Cost 3 (baseline, not a new regression): Han glued onto a PORT makes
  // linkify-it fail the whole match, so the string stays plain text — no link
  // at all. We deliberately do NOT build a recovery mechanism for this rare
  // shape; the add-a-space workaround applies. (Documented so a future reader
  // knows it was a decision, not an oversight.)
  it("cost 3: Han glued onto a port (x.com:8080吗) stays plain text", () => {
    expect(preprocessLinks("a https://x.com:8080吗 b")).toBe(
      "a https://x.com:8080吗 b",
    );
  });

  // Cost 4 — the ORIGINAL #537 report shape and the ONLY real-corpus sample
  // (s89 canonical row e6c14d53, 2026-07-03): Han glued onto a PATH. This PR is
  // host-only and does NOT fix it. Unlike the host, a path has no validity
  // oracle: legitimate path content (`/wiki/中国`) and a glued trailing particle
  // are character-identical, so it needs its own product/contract decision
  // (tracked under the still-open task #537). Not a regression — baseline
  // behavior is identical. Uses the VERBATIM canonical URL, per #640 (a
  // simplified shape must not stand in for the real triggering input).
  it("cost 4: Han glued onto a PATH (canonical github.com/…吗) is NOT handled", () => {
    expect(preprocessLinks("https://github.com/LRM-Teams/multica吗")).toBe(
      "[https://github.com/LRM-Teams/multica吗](https://github.com/LRM-Teams/multica吗)",
    );
  });
});
