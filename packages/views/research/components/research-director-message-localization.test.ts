import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import en from "../../locales/en/research.json";
import zh from "../../locales/zh-Hans/research.json";

const pageSource = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "research-session-page.tsx"),
  "utf8",
);

describe("Research Director action-message localization", () => {
  it("keeps the complete action-message keyset in both supported locales", () => {
    expect(Object.keys(en.director_messages).sort()).toEqual(
      Object.keys(zh.director_messages).sort(),
    );
    expect(Object.keys(en.director_messages)).toHaveLength(8);
  });

  it("routes every Director control message through the locale bundle", () => {
    for (const key of Object.keys(en.director_messages)) {
      expect(pageSource).toContain(`$.director_messages.${key}`);
    }
    expect(pageSource).not.toMatch(/[\u4e00-\u9fff]/u);
  });

  it("preserves the same interpolation fields across locales", () => {
    const placeholders = (value: string) =>
      [...value.matchAll(/\{\{(\w+)\}\}/g)].map((match) => match[1]).sort();

    for (const key of Object.keys(en.director_messages) as Array<
      keyof typeof en.director_messages
    >) {
      expect(placeholders(en.director_messages[key])).toEqual(
        placeholders(zh.director_messages[key]),
      );
    }
  });
});
