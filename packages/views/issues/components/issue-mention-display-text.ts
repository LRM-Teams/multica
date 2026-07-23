/**
 * Visible primary ink for an issue mention in message bodies (LRM-508).
 *
 * Title-first — same口径 as LRM-423 system events. Never paint `LRM-xxx` or a
 * bare UUID as the main token (overrides LRM-493's identifier-primary).
 *
 * Missing / empty title → `null` (explicit empty; LRM-238 forbids silent
 * identifier/UUID stand-ins). Callers render nothing rather than fake ink.
 */
export function resolveIssueMentionDisplayText(
  title: string | undefined | null,
): string | null {
  const trimmed = title?.trim();
  return trimmed || null;
}
