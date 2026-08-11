import type { AgentRuntime } from "@multica/core/types";
import { isRuntimeUsableForUser } from "../runtime-usability";

export function runtimePickerOptions(
  runtimes: AgentRuntime[],
  currentUserId: string | null,
): AgentRuntime[] {
  return runtimes
    .filter((runtime) => isRuntimeUsableForUser(runtime, currentUserId))
    .toSorted((a, b) => {
      const aMine = a.owner_id === currentUserId;
      const bMine = b.owner_id === currentUserId;
      if (aMine && !bMine) return -1;
      if (!aMine && bMine) return 1;
      return 0;
    });
}
