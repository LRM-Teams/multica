/** Chat drawer / FAB body modes (LRM-992). Aligns with fleet-strip four-state language. */
export type ChatDrawerMode = "empty" | "loading" | "running" | "error";

export function resolveChatDrawerMode(
  itemCount: number,
  sessionStatus?: string | null,
  opts?: { loading?: boolean; error?: boolean | string | null },
): ChatDrawerMode {
  if (opts?.error) return "error";
  if (opts?.loading) return "loading";
  if (itemCount <= 0) {
    // In-flight with an empty feed → assembling, not a permanent gray stub.
    if (sessionStatus === "running" || sessionStatus === "paused") return "loading";
    return "empty";
  }
  return "running";
}
