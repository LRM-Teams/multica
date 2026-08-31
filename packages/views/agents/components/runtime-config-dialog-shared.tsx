import type { ReactNode } from "react";
import type { AgentRuntime, MemberWithUser } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { DialogFooter } from "@multica/ui/components/ui/dialog";
import { RuntimeConfigFields } from "./runtime-config-fields";
import { useRuntimeConfigSelection } from "./use-runtime-config-selection";

type RuntimeConfigSelection = ReturnType<typeof useRuntimeConfigSelection>;

type RuntimeConfigSelectionFieldsProps = {
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  selection: RuntimeConfigSelection;
  disabled: boolean;
};

export function RuntimeConfigSelectionFields({
  runtimes,
  members,
  currentUserId,
  selection,
  disabled,
}: RuntimeConfigSelectionFieldsProps) {
  return (
    <RuntimeConfigFields
      runtimes={runtimes}
      members={members}
      currentUserId={currentUserId}
      machineId={selection.machineId}
      onMachineSelect={selection.selectMachine}
      machineRuntimes={selection.machineRuntimes}
      runtimeId={selection.runtimeId}
      onRuntimeSelect={selection.selectRuntime}
      model={selection.model}
      onModelChange={selection.selectModel}
      thinkingLevel={selection.thinkingLevel}
      onThinkingChange={selection.selectThinking}
      modelRequired
      disabled={disabled}
    />
  );
}

type RuntimeConfigDialogFooterProps = {
  saving: boolean;
  disabled?: boolean;
  cancelLabel: ReactNode;
  saveLabel: ReactNode;
  saveTestId: string;
  onCancel: () => void;
  onSave: () => void;
};

export function RuntimeConfigDialogFooter({
  saving,
  disabled = false,
  cancelLabel,
  saveLabel,
  saveTestId,
  onCancel,
  onSave,
}: RuntimeConfigDialogFooterProps) {
  return (
    <DialogFooter>
      <Button
        type="button"
        variant="ghost"
        disabled={saving}
        onClick={onCancel}
      >
        {cancelLabel}
      </Button>
      <Button
        type="button"
        disabled={saving || disabled}
        data-testid={saveTestId}
        onClick={onSave}
      >
        {saveLabel}
      </Button>
    </DialogFooter>
  );
}
