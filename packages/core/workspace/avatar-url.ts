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
 * New agents persist one concrete path from this pool at creation time.
 * Existing agents render their persisted value; this array is a picker/source
 * manifest, never a render-time fallback.
 */
export const AGENT_AVATAR_PRESETS: readonly string[] = Array.from(
  { length: 24 },
  (_, i) => `/agent-avatars/human-${String(i + 1).padStart(2, "0")}.jpg`,
);
