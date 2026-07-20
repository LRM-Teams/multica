import { api } from "../api";

export function resolvePublicFileUrlWithBase(rawUrl: string | null | undefined, baseUrl: string): string | null {
  if (!rawUrl) return null;
  if (!rawUrl.startsWith("/")) return rawUrl;
  if (rawUrl.startsWith("/agent-avatars/")) return rawUrl;
  const trimmedBaseUrl = baseUrl.replace(/\/+$/, "");
  return `${trimmedBaseUrl}${rawUrl}`;
}

export function resolvePublicFileUrl(rawUrl: string | null | undefined): string | null {
  return resolvePublicFileUrlWithBase(rawUrl, api.getBaseUrl());
}

/**
 * The shared 24-photo default-avatar pool (assets live at
 * `apps/web/public/agent-avatars/human-01..24.jpg`). Frank's #451 ruling:
 * retire the bot glyph / random colors; give an actor with no self-chosen
 * avatar one of these real-person photos.
 *
 * These are ONLY a display-layer default — never persisted (see
 * `defaultAgentAvatarPath`). Parker's rule: `avatar_url` in the DB must mean
 * "a human explicitly chose this"; a machine-assigned default is not data.
 * Because the default is computed, growing/reordering this pool re-flows every
 * default avatar automatically with zero stored-value anchoring.
 */
export const AGENT_AVATAR_PRESETS: readonly string[] = Array.from(
  { length: 24 },
  (_, i) => `/agent-avatars/human-${String(i + 1).padStart(2, "0")}.jpg`,
);

/**
 * Deterministic 32-bit string hash (djb2). Same input → same output across
 * reloads and platforms, so an actor id maps to a stable pool slot without any
 * stored state. Not cryptographic — only used for even, stable bucketing.
 */
export function stableHash(input: string): number {
  let hash = 5381;
  for (let i = 0; i < input.length; i++) {
    hash = ((hash << 5) + hash + input.charCodeAt(i)) | 0;
  }
  return hash >>> 0;
}

/**
 * The default avatar path for an actor with no self-chosen avatar, keyed by
 * the STABLE actor id (not name — names change, ids don't). Deterministic and
 * stateless: same id always resolves to the same photo, different ids spread
 * across the pool. Returns a root-relative `/agent-avatars/...` path (web
 * serves it directly; callers on other platforms resolve against their host).
 *
 * ⛔ Never write this to `avatar_url` — it is a render-time default only.
 */
export function defaultAgentAvatarPath(actorId: string): string {
  const index = stableHash(actorId) % AGENT_AVATAR_PRESETS.length;
  return AGENT_AVATAR_PRESETS[index]!;
}
