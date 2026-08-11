import type { RuntimeDevice } from "@multica/core/types";
import type { RuntimeMachine } from "../../runtimes/components/runtime-machines";

export function machineForRuntime(
  runtime: RuntimeDevice | null | undefined,
  machines: RuntimeMachine[],
): RuntimeMachine | null {
  if (!runtime) return null;
  return machines.find((m) => m.runtimes.some((r) => r.id === runtime.id)) ?? null;
}

export function firstRuntimeMachine(
  machines: RuntimeMachine[],
): RuntimeMachine | null {
  return machines.find((machine) => machine.runtimes.length > 0) ?? machines[0] ?? null;
}

export function firstRuntimeIdOnMachine(
  machine: RuntimeMachine | null | undefined,
): string {
  return machine?.runtimes[0]?.id ?? "";
}

/** True when runtimeId is one of the machine's provider processes. */
export function runtimeBelongsToMachine(
  runtimeId: string,
  machine: RuntimeMachine | null | undefined,
): boolean {
  if (!runtimeId || !machine) return false;
  return machine.runtimes.some((runtime) => runtime.id === runtimeId);
}
