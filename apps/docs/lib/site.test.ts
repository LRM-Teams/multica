import { beforeEach, describe, expect, it, vi } from "vitest";

const existingDocs = vi.hoisted(() => new Set<string>());

vi.mock("node:fs", () => ({
  existsSync: vi.fn((path: string) => {
    const normalized = path.replaceAll("\\", "/");
    return [...existingDocs].some((suffix) => normalized.endsWith(suffix));
  }),
}));

const pages = new Map<string, { url: string }>([
  ["en:", { url: "/" }],
  ["zh:", { url: "/zh" }],
  ["en:agents", { url: "/agents" }],
  ["zh:agents", { url: "/zh/agents" }],
]);

vi.mock("@/lib/source", () => ({
  source: {
    getPage: vi.fn((slugs: string[], lang: string) => {
      return pages.get(`${lang}:${slugs.join("/")}`) ?? null;
    }),
  },
}));

beforeEach(() => {
  existingDocs.clear();
  existingDocs.add("index.mdx");
  existingDocs.add("index.zh.mdx");
  existingDocs.add("agents.mdx");
  existingDocs.add("agents.zh.mdx");
});

describe("docsAlternates", () => {
  it("includes only shipped locales", async () => {
    const { docsAlternates } = await import("./site");

    expect(docsAlternates(["agents"])).toEqual({
      canonical: "https://www.leagent.me/docs/agents",
      languages: {
        en: "https://www.leagent.me/docs/agents",
        zh: "https://www.leagent.me/docs/zh/agents",
        "x-default": "https://www.leagent.me/docs/agents",
      },
    });
  });

  it("keeps the locale root alternates limited to real localized MDX pages", async () => {
    const { docsAlternates } = await import("./site");

    expect(docsAlternates([])).toEqual({
      canonical: "https://www.leagent.me/docs",
      languages: {
        en: "https://www.leagent.me/docs",
        zh: "https://www.leagent.me/docs/zh",
        "x-default": "https://www.leagent.me/docs",
      },
    });
  });
});
