/** Fleet strip body modes (LRM-980). */
export type FleetStripMode = "empty" | "loading" | "running" | "done";

export function resolveFleetStripMode(
  memberCount: number,
  sessionStatus?: string | null,
  loading?: boolean,
): FleetStripMode {
  if (loading) return "loading";
  if (memberCount <= 0) {
    // In-flight with no roster yet → assembling, not a permanent gray stub.
    if (sessionStatus === "running" || sessionStatus === "paused") return "loading";
    return "empty";
  }
  if (
    sessionStatus === "completed" ||
    sessionStatus === "awaiting_user_confirm" ||
    sessionStatus === "archived"
  ) {
    return "done";
  }
  return "running";
}
