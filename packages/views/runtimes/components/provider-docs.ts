/**
 * Official site / docs URLs for the task #17 code-agent catalog.
 * Verified by Iris 2026-08-01; catalog narrowed to six by Frank/Iris.
 */
export const PROVIDER_DOCS_URLS: Readonly<Record<string, string | null>> = {
  claude: "https://code.claude.com/docs",
  codex: "https://developers.openai.com/codex/cli",
  opencode: "https://opencode.ai/docs/cli/",
  pi: "https://pi.dev/docs",
  cursor: "https://cursor.com/docs/cli/installation",
  grok: "https://docs.x.ai/build/overview",
};

export function providerDocsUrl(provider: string): string | null {
  return PROVIDER_DOCS_URLS[provider] ?? null;
}
