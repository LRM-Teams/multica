/**
 * Returns a canonical web URL suitable for an anchor, or null for unsupported
 * schemes and malformed input. Research source URLs are backend facts, but
 * they are not automatically safe navigation targets.
 */
export function safeSourceUrl(value: unknown): string | null {
  if (typeof value !== "string" || !value.trim()) return null;
  try {
    const url = new URL(value.trim());
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return value.trim();
  } catch {
    return null;
  }
}

export function sourceHost(value: unknown): string {
  const safe = safeSourceUrl(value);
  if (!safe) return "";
  return new URL(safe).hostname.replace(/^www\./, "");
}
