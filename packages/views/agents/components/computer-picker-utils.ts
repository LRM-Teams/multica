import type { RuntimeDevice } from "@multica/core/types";
import type { RuntimeMachine } from "../../runtimes/components/runtime-machines";

export function machineForRuntime(
  runtime: RuntimeDevice | null | undefined,
  machines: RuntimeMachine[],
): RuntimeMachine | null {
  if (!runtime) return null;
  return machines.find((m) => m.runtimes.some((r) => r.id === runtime.id)) ?? null;
}

export function firstUsableMachine(
  machines: RuntimeMachine[],
  currentUserId: string | null,
): RuntimeMachine | null {
  for (const machine of machines) {
    if (
      machine.runtimes.some((r) => {
        if (!currentUserId) return true;
        if (r.owner_id === currentUserId) return true;
        return r.visibility === "public";
      })
    ) {
      return machine;
    }
  }
  return machines[0] ?? null;
}

/** Prefer a usable runtime on the machine; otherwise first runtime id. */
export function firstUsableRuntimeIdOnMachine(
  machine: RuntimeMachine | null | undefined,
  currentUserId: string | null,
): string {
  if (!machine) return "";
  const usable = machine.runtimes.find((r) => {
    if (!currentUserId) return true;
    if (r.owner_id === currentUserId) return true;
    return r.visibility === "public";
  });
  return usable?.id ?? machine.runtimes[0]?.id ?? "";
}
