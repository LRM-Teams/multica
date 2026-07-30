/**
 * Mobile relative-time formatter. Mirrors the ladder in
 * packages/views/i18n/use-time-ago.ts (the LRM-763 <Time kind="relative">
 * contract) so relative timestamps read identically across web/desktop and
 * mobile (Behavioral parity rule in apps/mobile/CLAUDE.md). The web version
 * is i18n-driven via useT; mobile v1 is English-only — when mobile ships
 * i18n, mirror that structure. No `toLocale*` calls — the bucket ladder is
 * the contract, identical on every device.
 */
export function formatTimeAgo(valueMs: number, nowMs: number): string {
  if (Number.isNaN(valueMs)) return "";
  const minutes = Math.floor((nowMs - valueMs) / 60000);
  // Covers future timestamps / clock skew — never "-3m ago".
  if (minutes < 1) return "Just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

export function timeAgo(dateStr: string): string {
  return formatTimeAgo(new Date(dateStr).getTime(), Date.now());
}
