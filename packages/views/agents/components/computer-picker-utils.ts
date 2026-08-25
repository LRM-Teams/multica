import type { RuntimeDevice } from "@multica/core/types";
import type { RuntimeMachine } from "../../runtimes/components/runtime-machines";
import { isRuntimeUsableForUser } from "./runtime-usability";

export function machineForRuntime(
  runtime: RuntimeDevice | null | undefined,
  machines: RuntimeMachine[],
): RuntimeMachine | null {
  if (!runtime) return null;
  return machines.find((m) => m.runtimes.some((r) => r.id === runtime.id)) ?? null;
}

export function firstRuntimeMachine(
  machines: RuntimeMachine[],
  currentUserId?: string | null,
  bindableIds?: ReadonlySet<string> | null,
): RuntimeMachine | null {
  if (currentUserId !== undefined) {
    for (const machine of machines) {
      if (machine.runtimes.some((r) => isRuntimeUsableForUser(r, currentUserId, bindableIds))) {
        return machine;
      }
    }
  }
  return machines.find((machine) => machine.runtimes.length > 0) ?? machines[0] ?? null;
}

/** Prefer a usable runtime on the machine; otherwise first runtime id. */
export function firstRuntimeIdOnMachine(
  machine: RuntimeMachine | null | undefined,
  currentUserId?: string | null,
  bindableIds?: ReadonlySet<string> | null,
): string {
  if (!machine) return "";
  if (currentUserId !== undefined) {
    const usable = machine.runtimes.find((r) =>
      isRuntimeUsableForUser(r, currentUserId, bindableIds),
    );
    if (usable) return usable.id;
  }
  return machine.runtimes[0]?.id ?? "";
}

/** True when runtimeId is one of the machine's provider processes. */
export function runtimeBelongsToMachine(
  runtimeId: string,
  machine: RuntimeMachine | null | undefined,
): boolean {
  if (!runtimeId || !machine) return false;
  return machine.runtimes.some((runtime) => runtime.id === runtimeId);
}
