"use client";

import type { MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { ComputerPicker } from "./computer-picker";
import { RuntimePicker } from "./runtime-picker";
import { ModelDropdown } from "./model-dropdown";
import { ThinkingDropdown } from "./thinking-dropdown";

/**
 * Site-wide execution bind form: Computer → Runtime → Model → Reasoning.
 *
 * Visuals match Create Agent (bordered full-width pickers). Parents own
 * cascade state via `useExecutionSelection` or equivalent.
 */
export function ExecutionConfigFields({
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
  machineId,
  onMachineSelect,
  machineRuntimes,
  runtimeId,
  onRuntimeSelect,
  runtimeOnline,
  model,
  onModelChange,
  thinkingLevel,
  onThinkingChange,
  modelRequired = false,
  autoSelectFirstModel = false,
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
  runtimeOnline: boolean;
  model: string;
  onModelChange: (model: string) => void;
  thinkingLevel: string;
  onThinkingChange: (level: string) => void;
  modelRequired?: boolean;
  autoSelectFirstModel?: boolean;
  disabled?: boolean;
}) {
  return (
    <div className="flex flex-col gap-3" data-testid="execution-config-fields">
      <ComputerPicker
        runtimes={runtimes}
        runtimesLoading={runtimesLoading}
        currentUserId={currentUserId}
        selectedMachineId={machineId}
        onSelect={onMachineSelect}
      />
      <RuntimePicker
        runtimes={machineRuntimes}
        runtimesLoading={runtimesLoading}
        members={members}
        currentUserId={currentUserId}
        selectedRuntimeId={runtimeId}
        onSelect={onRuntimeSelect}
      />
      <ModelDropdown
        runtimeId={runtimeId || null}
        runtimeOnline={runtimeOnline && !!runtimeId}
        value={model}
        onChange={onModelChange}
        disabled={disabled || !runtimeId}
        required={modelRequired}
        autoSelectFirst={autoSelectFirstModel}
      />
      <ThinkingDropdown
        runtimeId={runtimeId || null}
        runtimeOnline={runtimeOnline && !!runtimeId}
        model={model}
        value={thinkingLevel}
        onChange={onThinkingChange}
        disabled={disabled || !runtimeId || !model}
      />
    </div>
  );
}
