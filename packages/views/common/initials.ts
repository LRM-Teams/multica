/**
 * Derive up-to-two-letter initials from a display name, for avatar fallbacks.
 * Single-word names take their first two letters; multi-word names take the
 * first letter of the first and last word. Empty input yields "?".
 */
export function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]!.charAt(0) + parts[parts.length - 1]!.charAt(0)).toUpperCase();
}
