/**
 * Official site / docs URLs for known code-agent providers (task #17).
 * Verified by Iris 2026-08-01 in #prj-frontend:9516b2d5.
 * openclaw omitted — Frank 08-01: "OpenClaw不用管了".
 */
export const PROVIDER_DOCS_URLS: Readonly<Record<string, string | null>> = {
  claude: "https://code.claude.com/docs",
  codebuddy: "https://www.codebuddy.ai/docs/cli/installation",
  codex: "https://developers.openai.com/codex/cli",
  opencode: "https://opencode.ai/docs/cli/",
  hermes: "https://hermes-agent.nousresearch.com/docs/user-guide/cli",
  pi: "https://pi.dev/docs",
  copilot:
    "https://docs.github.com/en/copilot/how-tos/copilot-cli/install-copilot-cli",
  cursor: "https://cursor.com/docs/cli/installation",
  kimi: "https://github.com/MoonshotAI/kimi-code",
  kiro: "https://kiro.dev/cli/",
  gemini: "https://github.com/google-gemini/gemini-cli",
  antigravity: "https://antigravity.google/product/antigravity-cli",
  grok: "https://docs.x.ai/build/overview",
};

export function providerDocsUrl(provider: string): string | null {
  return PROVIDER_DOCS_URLS[provider] ?? null;
}
