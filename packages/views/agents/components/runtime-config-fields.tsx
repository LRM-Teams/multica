"use client";

import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import type { MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { ComputerPicker } from "./computer-picker";
import { RuntimePicker } from "./runtime-picker";
import { ModelDropdown } from "./model-dropdown";
import { ThinkingDropdown } from "./thinking-dropdown";

/**
 * Site-wide runtime config form: Computer → Runtime → Model → Reasoning.
 *
 * Visuals match Create Agent (bordered full-width pickers). Parents own
 * cascade state via `useRuntimeConfigSelection` or equivalent.
 *
 * Selecting a Runtime immediately prefetches the model catalog (daemon
 * list-models scan) so Model/Reasoning do not wait for the user to open
 * the Model menu.
 */
export function RuntimeConfigFields({
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
  machineId,
  onMachineSelect,
  machineRuntimes,
  runtimeId,
  onRuntimeSelect,
  model,
  onModelChange,
  thinkingLevel,
  onThinkingChange,
  modelRequired = false,
  lockComputer = false,
  disabled = false,
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  members: MemberWithUser[];
  currentUserId: string | null;
  machineId: string;
  onMachineSelect: (machineId: string) => void;
  /** Runtimes scoped to the selected computer only. */
  machineRuntimes: RuntimeDevice[];
  runtimeId: string;
  onRuntimeSelect: (runtimeId: string) => void;
  model: string;
  onModelChange: (model: string) => void;
  thinkingLevel: string;
  onThinkingChange: (level: string) => void;
  modelRequired?: boolean;
  /** Computer is preselected and cannot be changed. */
  lockComputer?: boolean;
  disabled?: boolean;
}) {
  const queryClient = useQueryClient();

  // Kick off list-models as soon as Runtime is chosen (not on Model open).
  useEffect(() => {
    if (!runtimeId) return;
    void queryClient.prefetchQuery(runtimeModelsOptions(runtimeId));
  }, [runtimeId, queryClient]);

  return (
    <div
      className="flex min-w-0 flex-col gap-2.5"
      data-testid="runtime-config-fields"
    >
      <ComputerPicker
        runtimes={runtimes}
        runtimesLoading={runtimesLoading}
        currentUserId={currentUserId}
        selectedMachineId={machineId}
        onSelect={onMachineSelect}
        disabled={disabled || lockComputer}
      />
      <RuntimePicker
        runtimes={machineRuntimes}
        runtimesLoading={runtimesLoading}
        members={members}
        currentUserId={currentUserId}
        selectedRuntimeId={runtimeId}
        onSelect={onRuntimeSelect}
        disabled={disabled}
      />
      <ModelDropdown
        runtimeId={runtimeId || null}
        value={model}
        onChange={onModelChange}
        disabled={disabled || !runtimeId}
        required={modelRequired}
      />
      <ThinkingDropdown
        runtimeId={runtimeId || null}
        model={model}
        value={thinkingLevel}
        onChange={onThinkingChange}
        disabled={disabled || !runtimeId}
      />
    </div>
  );
}
