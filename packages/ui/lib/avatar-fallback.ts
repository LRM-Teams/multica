/**
 * Shared avatar fallback helpers (LRM-201).
 *
 * Missing / failed / unavailable faces use a single glyph + stable tone —
 * never a flat gray two-letter disc.
 */

/** Single glyph — surname / Latin initial. Avoids packing two CJK chars. */
export function avatarGlyph(name: string): string {
  const c = name.trim().charAt(0);
  if (!c) return "?";
  return /[a-z]/i.test(c) ? c.toUpperCase() : c;
}

/** Stable Slack-ish fallback tones keyed by actor id / name. */
const AVATAR_TONE_CLASSES = [
  "bg-[#4a154b] text-white",
  "bg-[#1264a3] text-white",
  "bg-[#2bac76] text-white",
  "bg-[#e01e5a] text-white",
  "bg-[#ecb22e] text-[#3d2e00]",
  "bg-[#1d9bd1] text-white",
  "bg-[#6b4eff] text-white",
] as const;

export function avatarToneClass(seed: string): string {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_TONE_CLASSES[h % AVATAR_TONE_CLASSES.length]!;
}
