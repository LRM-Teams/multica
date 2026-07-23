import { api } from "../api";

/**
 * Cache-bust token for bundled agent preset faces (LRM-218).
 *
 * Browsers (esp. mobile Safari) can pin a prior 404 for `/agent-avatars/*`
 * across deploys; payloads and static files are fine but `<img>` keeps failing
 * into the glyph fallback. Bumping this forces a fresh fetch without waiting
 * for cache expiry.
 */
export const AGENT_AVATAR_ASSET_VERSION = "lrm218";

function withAgentAvatarCacheBust(path: string): string {
  if (!path.startsWith("/agent-avatars/")) return path;
  if (path.includes(`v=${AGENT_AVATAR_ASSET_VERSION}`)) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}v=${AGENT_AVATAR_ASSET_VERSION}`;
}

export function resolvePublicFileUrlWithBase(rawUrl: string | null | undefined, baseUrl: string): string | null {
  if (!rawUrl) return null;
  if (!rawUrl.startsWith("/")) return rawUrl;
  const trimmedBaseUrl = baseUrl.replace(/\/+$/, "");

  // Agent presets: always cache-bust. Empty base = web same-origin (Next
  // `public/`). Non-empty base = desktop / remote API — renderer has no
  // bundled pool, so presets must hit the API host like uploads do.
  if (rawUrl.startsWith("/agent-avatars/")) {
    const busted = withAgentAvatarCacheBust(rawUrl);
    if (!trimmedBaseUrl) return busted;
    return `${trimmedBaseUrl}${busted}`;
  }

  if (!trimmedBaseUrl) return rawUrl;
  return `${trimmedBaseUrl}${rawUrl}`;
}

export function resolvePublicFileUrl(rawUrl: string | null | undefined): string | null {
  // Optional-chain: unit tests often partial-mock `@multica/core/api` without
  // getBaseUrl. Empty base keeps relative `/uploads/...` paths as-is on web.
  return resolvePublicFileUrlWithBase(rawUrl, api.getBaseUrl?.() ?? "");
}

/**
 * The shared 24-photo default-avatar pool (assets live at
 * `apps/web/public/agent-avatars/human-01..24.jpg`). Frank's #451 ruling:
 * retire the bot glyph / random colors; give an actor with no self-chosen
 * avatar one of these real-person photos.
 *
 * New agents persist one concrete path from this pool at creation time.
 * Existing agents render their persisted value; this array is a picker/source
 * manifest, never a render-time fallback.
 */
export const AGENT_AVATAR_PRESETS: readonly string[] = Array.from(
  { length: 24 },
  (_, i) => `/agent-avatars/human-${String(i + 1).padStart(2, "0")}.jpg`,
);
