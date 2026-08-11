import type { AgentRuntime } from "@multica/core/types";

export function runtimePickerOptions(
  runtimes: AgentRuntime[],
  currentUserId: string | null,
): AgentRuntime[] {
  const isUsable = (runtime: AgentRuntime): boolean => {
    if (!currentUserId) return true;
    if (runtime.owner_id === currentUserId) return true;
    // Missing visibility fails closed as private (older clients / parse gaps).
    return runtime.visibility === "public";
  };
  return runtimes.filter(isUsable).toSorted((a, b) => {
    const aMine = a.owner_id === currentUserId;
    const bMine = b.owner_id === currentUserId;
    if (aMine && !bMine) return -1;
    if (!aMine && bMine) return 1;
    return 0;
  });
}
