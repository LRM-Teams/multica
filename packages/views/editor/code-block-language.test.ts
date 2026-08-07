import { afterEach, describe, expect, it } from "vitest";
import {
  INSERTABLE_CODE_BLOCK_LANGUAGES,
  getLastInsertedCodeBlockLanguage,
  setLastInsertedCodeBlockLanguage,
} from "./code-block-language";

const LAST_CODE_BLOCK_LANGUAGE_KEY = "multica:last-code-block-language";

afterEach(() => {
  window.localStorage.removeItem(LAST_CODE_BLOCK_LANGUAGE_KEY);
});

describe("INSERTABLE_CODE_BLOCK_LANGUAGES", () => {
  it("covers every renderable code language, including markdown and html", () => {
    // Regression: PR #2479 dropped markdown/html from the insertable set,
    // which also removed them from the toolbar dropdown and /code path.
    expect(
      [...INSERTABLE_CODE_BLOCK_LANGUAGES].sort(),
    ).toEqual(["html", "javascript", "markdown", "mermaid", "plaintext", "python"]);
  });
});

describe("setLastInsertedCodeBlockLanguage", () => {
  it("persists markdown and html verbatim instead of normalizing to plaintext", () => {
    expect(setLastInsertedCodeBlockLanguage("markdown")).toBe("markdown");
    expect(window.localStorage.getItem(LAST_CODE_BLOCK_LANGUAGE_KEY)).toBe("markdown");

    expect(setLastInsertedCodeBlockLanguage("html")).toBe("html");
    expect(window.localStorage.getItem(LAST_CODE_BLOCK_LANGUAGE_KEY)).toBe("html");
  });

  it("falls back to plaintext for unsupported languages", () => {
    expect(setLastInsertedCodeBlockLanguage("ruby")).toBe("plaintext");
    expect(window.localStorage.getItem(LAST_CODE_BLOCK_LANGUAGE_KEY)).toBe("plaintext");
  });
});

describe("getLastInsertedCodeBlockLanguage", () => {
  it("reads back a previously stored markdown selection", () => {
    window.localStorage.setItem(LAST_CODE_BLOCK_LANGUAGE_KEY, "markdown");
    expect(getLastInsertedCodeBlockLanguage()).toBe("markdown");
  });
});
