/** Cursor HTTP/2 keepalive / RetriableError dumps that must not render raw in the DM bubble. */
export function isTransportHangError(raw?: string | null): boolean {
  const lower = (raw ?? "").toLowerCase();
  if (lower.includes("keepalive ping timed out")) return true;
  return lower.includes("retriableerror") && lower.includes("http/2");
}
