import type { ComputerConnection } from "../types";

/**
 * True when none of the viewer's Computers have Machine Work Journal on.
 * Missing or drifted `work_journal_enabled` is treated as off.
 */
export function localMachineWorkUncollected(
  computers: readonly ComputerConnection[] | undefined,
  userId: string | undefined,
): boolean {
  if (typeof userId !== "string" || userId.length === 0) {
    return true;
  }
  return !(computers ?? []).some(
    (computer) => computer.owner_id === userId && computer.work_journal_enabled === true,
  );
}
