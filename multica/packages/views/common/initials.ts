/**
 * Avatar / initials helpers. Glyph + tone live in `@multica/ui` so ActorAvatar
 * and channel surfaces share one LRM-201 contract; this module re-exports them
 * and keeps the legacy two-letter `initialsOf` for non-avatar text chips.
 */

export { avatarGlyph, avatarToneClass } from "@multica/ui/lib/avatar-fallback";

/**
 * Derive up-to-two-letter initials from a display name.
 * Prefer `avatarGlyph` for avatar discs (single char + tone).
 */
export function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]!.charAt(0) + parts[parts.length - 1]!.charAt(0)).toUpperCase();
}
