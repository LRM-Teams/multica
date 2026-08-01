/**
 * Official site / docs URLs for known code-agent providers (task #17).
 *
 * Iris is verifying each URL before we ship live links — leave null until
 * confirmed. Rows without a URL omit the external-link button rather than
 * pointing at a guess.
 */
export const PROVIDER_DOCS_URLS: Readonly<Record<string, string | null>> = {
  claude: null, // TODO(iris): verify official docs URL
  codebuddy: null, // TODO(iris): verify official docs URL
  codex: null, // TODO(iris): verify official docs URL
  opencode: null, // TODO(iris): verify official docs URL
  openclaw: null, // TODO(iris): verify official docs URL
  hermes: null, // TODO(iris): verify official docs URL
  pi: null, // TODO(iris): verify official docs URL
  copilot: null, // TODO(iris): verify official docs URL
  cursor: null, // TODO(iris): verify official docs URL
  kimi: null, // TODO(iris): verify official docs URL
  kiro: null, // TODO(iris): verify official docs URL
  gemini: null, // TODO(iris): verify official docs URL
  antigravity: null, // TODO(iris): verify official docs URL
  grok: null, // TODO(iris): verify official docs URL
};

export function providerDocsUrl(provider: string): string | null {
  return PROVIDER_DOCS_URLS[provider] ?? null;
}
