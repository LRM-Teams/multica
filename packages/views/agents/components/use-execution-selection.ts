"use client";

import { useMemo, useState } from "react";
import type { RuntimeDevice } from "@multica/core/types";
import { buildRuntimeMachines } from "../../runtimes/components/runtime-machines";
import {
  firstRuntimeIdOnMachine,
  firstRuntimeMachine,
  machineForRuntime,
  runtimeBelongsToMachine,
} from "./computer-picker-utils";

type ModelScope = {
  runtimeId: string;
  model: string;
  thinkingLevel: string;
};

/**
 * Computer → Runtime → Model → Reasoning selection with cascade rules.
 * Shared by Create / Wendy / Profile edit so all surfaces share one machine.
 */
export function useExecutionSelection({
  runtimes,
  currentUserId,
  initialRuntimeId = "",
  initialModel = "",
  initialThinkingLevel = "",
  autoSeedMachine = true,
}: {
  runtimes: RuntimeDevice[];
  currentUserId: string | null;
  initialRuntimeId?: string;
  initialModel?: string;
  initialThinkingLevel?: string;
  /** Seed first machine when nothing is preselected (Create). Wendy sets false until online. */
  autoSeedMachine?: boolean;
}) {
  const machines = useMemo(
    () => buildRuntimeMachines(runtimes, { now: Date.now(), currentUserId }),
    [runtimes, currentUserId],
  );

  const [pickedMachineId, setPickedMachineId] = useState(() => {
    const initialRuntime = initialRuntimeId
      ? runtimes.find((r) => r.id === initialRuntimeId)
      : undefined;
    if (initialRuntime) {
      return machineForRuntime(initialRuntime, machines)?.id ?? "";
    }
    if (!autoSeedMachine) return "";
    return firstRuntimeMachine(machines, currentUserId)?.id ?? "";
  });

  const [pickedRuntimeId, setPickedRuntimeId] = useState(initialRuntimeId);
  const [modelScope, setModelScope] = useState<ModelScope>({
    runtimeId: initialRuntimeId,
    model: initialModel,
    thinkingLevel: initialThinkingLevel,
  });

  const selectedMachine =
    machines.find((m) => m.id === pickedMachineId) ??
    machineForRuntime(
      runtimes.find((runtime) => runtime.id === pickedRuntimeId),
      machines,
    ) ??
    (autoSeedMachine && !pickedMachineId && !pickedRuntimeId
      ? firstRuntimeMachine(machines, currentUserId)
      : null) ??
    null;
  const machineId = selectedMachine?.id ?? pickedMachineId;
  const machineRuntimes = selectedMachine?.runtimes ?? [];

  const runtimeId = runtimeBelongsToMachine(pickedRuntimeId, selectedMachine)
    ? pickedRuntimeId
    : pickedRuntimeId && !selectedMachine
      ? pickedRuntimeId
      : firstRuntimeIdOnMachine(selectedMachine, currentUserId);

  const selectedRuntime =
    (runtimes.find((r) => r.id === runtimeId) as RuntimeDevice | undefined) ??
    machineRuntimes.find((r) => r.id === runtimeId) ??
    null;
  // Scope model/thinking to the effective runtime without an effect.
  if (modelScope.runtimeId !== runtimeId) {
    setModelScope({ runtimeId, model: "", thinkingLevel: "" });
  }
  const model =
    modelScope.runtimeId === runtimeId ? modelScope.model : "";
  const thinkingLevel =
    modelScope.runtimeId === runtimeId ? modelScope.thinkingLevel : "";

  const selectMachine = (nextMachineId: string) => {
    if (nextMachineId === machineId) return;
    setPickedMachineId(nextMachineId);
    const next = machines.find((m) => m.id === nextMachineId) ?? null;
    const nextRuntimeId = firstRuntimeIdOnMachine(next, currentUserId);
    setPickedRuntimeId(nextRuntimeId);
    setModelScope({
      runtimeId: nextRuntimeId,
      model: "",
      thinkingLevel: "",
    });
  };

  const selectRuntime = (nextRuntimeId: string) => {
    if (nextRuntimeId === runtimeId) return;
    setPickedRuntimeId(nextRuntimeId);
    setModelScope({
      runtimeId: nextRuntimeId,
      model: "",
      thinkingLevel: "",
    });
  };

  const selectModel = (nextModel: string) => {
    if (nextModel === model) return;
    setModelScope({
      runtimeId,
      model: nextModel,
      thinkingLevel: "",
    });
  };

  const selectThinking = (next: string) => {
    setModelScope({
      runtimeId,
      model,
      thinkingLevel: next,
    });
  };

  const resetFrom = (next: {
    runtimeId: string;
    model: string;
    thinkingLevel: string;
  }) => {
    const rt = runtimes.find((r) => r.id === next.runtimeId);
    const machine = machineForRuntime(rt, machines);
    setPickedMachineId(machine?.id ?? "");
    setPickedRuntimeId(next.runtimeId);
    setModelScope({
      runtimeId: next.runtimeId,
      model: next.model,
      thinkingLevel: next.thinkingLevel,
    });
  };

  return {
    machines,
    machineId,
    machineRuntimes,
    runtimeId,
    selectedRuntime,
    model,
    thinkingLevel,
    selectMachine,
    selectRuntime,
    selectModel,
    selectThinking,
    resetFrom,
  };
}
