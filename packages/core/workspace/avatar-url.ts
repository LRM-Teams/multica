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
 * True for the pre-OSS local upload path (`/uploads/...`). After the S3
 * migration these URLs 404; they must not clobber a newer OSS/CDN face URL
 * in RQ message cache or the identity sticky cache (LRM-855).
 */
export function isLegacyUploadsAvatarUrl(url: string | null | undefined): boolean {
  if (!url) return false;
  if (url.startsWith("/uploads/")) return true;
  try {
    // Absolute https://host/uploads/... (or any origin) — path still legacy.
    return new URL(url).pathname.startsWith("/uploads/");
  } catch {
    return false;
  }
}

/**
 * Pick an author face URL when both sides have one. Incoming (server) wins
 * unless it is a legacy `/uploads/` path and the other side already has a
 * non-legacy URL (OSS/CDN) — then keep the good one (LRM-855).
 */
export function preferAuthorAvatarUrl(
  incoming: string | null | undefined,
  cached: string | null | undefined,
): string | null | undefined {
  if (incoming && cached) {
    if (isLegacyUploadsAvatarUrl(incoming) && !isLegacyUploadsAvatarUrl(cached)) {
      return cached;
    }
    return incoming;
  }
  return incoming || cached || undefined;
}

/** Immutable system presets stored in OSS and served through the product CDN.
 * New Agents persist one concrete absolute URL at creation time. The absolute
 * form is deliberate: old desktop clients must not resolve it against the
 * separately-hosted API origin. */
export const AGENT_AVATAR_PRESETS: readonly string[] = Array.from(
  { length: 15 },
  (_, i) => `https://cdn.leagent.me/agent-avatars/v2/agent-${String(i + 1).padStart(2, "0")}.png`,
);
