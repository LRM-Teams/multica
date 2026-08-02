/** Delivery (sources & report) body modes (LRM-993). Aligns with chat-drawer / fleet-strip. */
export type DeliveryMode = "empty" | "loading" | "running" | "error";

export function resolveDeliveryMode(
  itemCount: number,
  sessionStatus?: string | null,
  opts?: { loading?: boolean; error?: boolean | string | null },
): DeliveryMode {
  if (opts?.error) return "error";
  if (opts?.loading) return "loading";
  if (itemCount <= 0) {
    // In-flight with no sources/report yet → assembling, not a permanent gray stub.
    if (sessionStatus === "running" || sessionStatus === "paused") return "loading";
    return "empty";
  }
  return "running";
}

/** Count scannable delivery items (report body and/or weighted sources). */
export function deliveryContentCount(
  report:
    | { content_md?: string | null; structured?: unknown }
    | null
    | undefined,
  sourceCount: number,
): number {
  const hasMd = Boolean(report?.content_md?.trim());
  const structured = report?.structured;
  const hasStructured =
    structured != null &&
    typeof structured === "object" &&
    !Array.isArray(structured) &&
    Object.keys(structured as object).length > 0;
  return (hasMd || hasStructured ? 1 : 0) + Math.max(0, sourceCount);
}
