import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import {
  INSERTABLE_CODE_BLOCK_LANGUAGES,
  getLastInsertedCodeBlockLanguage,
  setLastInsertedCodeBlockLanguage,
} from "./code-block-language";

// The toolbar language dropdown and the `/code` slash command both derive
// their offered languages from INSERTABLE_CODE_BLOCK_LANGUAGES. Regression
// for LRM-1492: PR #2479 shrank this set to 4 (dropping markdown/html) which
// made it impossible to create or convert markdown/html blocks via UI.
describe("INSERTABLE_CODE_BLOCK_LANGUAGES", () => {
  it("keeps every fully-supported block language, including markdown and html", () => {
    expect(INSERTABLE_CODE_BLOCK_LANGUAGES).toEqual([
      "plaintext",
      "markdown",
      "python",
      "javascript",
      "html",
      "mermaid",
    ]);
  });
});

describe("set/getLastInsertedCodeBlockLanguage", () => {
  const storage = new Map<string, string>();

  beforeEach(() => {
    storage.clear();
    vi.stubGlobal("window", {
      localStorage: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
      },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each(["plaintext", "markdown", "python", "javascript", "html", "mermaid"] as const)(
    "persists and reads back %s so /code recreates it",
    (language) => {
      expect(setLastInsertedCodeBlockLanguage(language)).toBe(language);
      expect(getLastInsertedCodeBlockLanguage()).toBe(language);
    },
  );

  it("falls back to plaintext for unrecognized languages", () => {
    expect(setLastInsertedCodeBlockLanguage("sql")).toBe("plaintext");
    expect(getLastInsertedCodeBlockLanguage()).toBe("plaintext");
  });
});
