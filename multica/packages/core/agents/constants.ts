// User-facing limits enforced symmetrically on the front-end (UI counter +
// disabled save) and the back-end (handler validation + DB CHECK constraint).
// Kept in core so both apps and the test suite read from one source.
export const AGENT_DESCRIPTION_MAX_LENGTH = 255;

// The stable `@handle` (`Agent.name`). ASCII grammar mirrored EXACTLY from the
// backend validator on `PUT /api/agents/{id}` (`username` field): lowercase
// letters and digits in dash-separated segments — NO leading/trailing hyphen and
// NO consecutive hyphens. Max 32 chars. Kept in core so the FE client-side check
// and any future callers read the SAME grammar the server enforces — the server
// is still authoritative (it also owns uniqueness → 409).
export const AGENT_USERNAME_REGEX = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
export const AGENT_USERNAME_MAX_LENGTH = 32;

export type AgentUsernameError = "empty" | "too_long" | "invalid_chars";

/**
 * Client-side grammar check for an agent `@handle`. Returns a machine-readable
 * error code (so callers own the i18n message) or `null` when the value is a
 * syntactically valid handle. Does NOT check uniqueness — that is server-only
 * and surfaces as a 409 on save.
 */
export function validateAgentUsername(value: string): AgentUsernameError | null {
  const v = value.trim();
  if (v.length === 0) return "empty";
  if (v.length > AGENT_USERNAME_MAX_LENGTH) return "too_long";
  if (!AGENT_USERNAME_REGEX.test(v)) return "invalid_chars";
  return null;
}
