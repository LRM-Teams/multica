const LAST_CODE_BLOCK_LANGUAGE_KEY = "multica:last-code-block-language";

export const INSERTABLE_CODE_BLOCK_LANGUAGES = [
  "plaintext",
  "markdown",
  "python",
  "javascript",
  "html",
  "mermaid",
] as const;

export type InsertableCodeBlockLanguage = (typeof INSERTABLE_CODE_BLOCK_LANGUAGES)[number];

const INSERTABLE_LANGUAGE_SET = new Set<string>(INSERTABLE_CODE_BLOCK_LANGUAGES);

function normalizeInsertableCodeBlockLanguage(value: string | null | undefined): InsertableCodeBlockLanguage {
  if (value && INSERTABLE_LANGUAGE_SET.has(value)) return value as InsertableCodeBlockLanguage;
  return "plaintext";
}

export function getLastInsertedCodeBlockLanguage(): InsertableCodeBlockLanguage {
  if (typeof window === "undefined") return "plaintext";
  return normalizeInsertableCodeBlockLanguage(window.localStorage.getItem(LAST_CODE_BLOCK_LANGUAGE_KEY));
}

export function setLastInsertedCodeBlockLanguage(language: string): InsertableCodeBlockLanguage {
  const normalized = normalizeInsertableCodeBlockLanguage(language);
  if (typeof window !== "undefined") {
    window.localStorage.setItem(LAST_CODE_BLOCK_LANGUAGE_KEY, normalized);
  }
  return normalized;
}
