"use client";

import { useMemo, useState } from "react";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { ExecutionConfigFields } from "./execution-config-fields";
import { useExecutionSelection } from "./use-execution-selection";

/**
 * LRM-1351 + site-wide Computer → Runtime → Model → Reasoning edit dialog.
 * Draft edits stay local; Save issues a single `onSave` patch (at most one
 * restart via `useUpdateAgent`). Cancel / Esc / mask discard the draft.
 *
 * Body remounts when the dialog opens (conditional render) so selection
 * always seeds from the latest agent snapshot.
 */
export function RuntimeConfigDialog({
  agent,
  open,
  onOpenChange,
  runtimes,
  members,
  currentUserId,
  onSave,
}: {
  agent: Agent;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  onSave: (patch: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const [saving, setSaving] = useState(false);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (saving) return;
        onOpenChange(next);
      }}
    >
      <DialogContent
        className="max-w-md"
        data-testid="agent-runtime-config-dialog"
      >
        <DialogHeader>
          <DialogTitle className="text-sm">
            {t(($) => $.execution_config.dialog_title)}
          </DialogTitle>
        </DialogHeader>

        {open ? (
          <RuntimeConfigDialogBody
            key={`${agent.id}:${agent.runtime_id}:${agent.model ?? ""}:${agent.thinking_level ?? ""}`}
            agent={agent}
            runtimes={runtimes}
            members={members}
            currentUserId={currentUserId}
            saving={saving}
            setSaving={setSaving}
            onOpenChange={onOpenChange}
            onSave={onSave}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function RuntimeConfigDialogBody({
  agent,
  runtimes,
  members,
  currentUserId,
  saving,
  setSaving,
  onOpenChange,
  onSave,
}: {
  agent: Agent;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  saving: boolean;
  setSaving: (v: boolean) => void;
  onOpenChange: (open: boolean) => void;
  onSave: (patch: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const selection = useExecutionSelection({
    runtimes,
    currentUserId,
    initialRuntimeId: agent.runtime_id,
    initialModel: agent.model ?? "",
    initialThinkingLevel: agent.thinking_level ?? "",
    autoSeedMachine: true,
  });

  const dirty = useMemo(() => {
    return (
      selection.runtimeId !== agent.runtime_id ||
      selection.model !== (agent.model ?? "") ||
      selection.thinkingLevel !== (agent.thinking_level ?? "")
    );
  }, [agent, selection.runtimeId, selection.model, selection.thinkingLevel]);

  const handleSave = async () => {
    if (!dirty) {
      onOpenChange(false);
      return;
    }
    if (!selection.runtimeId || !selection.model.trim()) return;
    const patch: Record<string, unknown> = {};
    if (selection.runtimeId !== agent.runtime_id) {
      patch.runtime_id = selection.runtimeId;
    }
    if (selection.model !== (agent.model ?? "")) {
      patch.model = selection.model;
    }
    if (selection.thinkingLevel !== (agent.thinking_level ?? "")) {
      patch.thinking_level = selection.thinkingLevel;
    }
    setSaving(true);
    try {
      await onSave(patch);
      onOpenChange(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <div className="py-1">
        <ExecutionConfigFields
          runtimes={runtimes}
          members={members}
          currentUserId={currentUserId}
          machineId={selection.machineId}
          onMachineSelect={selection.selectMachine}
          machineRuntimes={selection.machineRuntimes}
          runtimeId={selection.runtimeId}
          onRuntimeSelect={selection.selectRuntime}
          runtimeOnline={selection.runtimeOnline}
          model={selection.model}
          onModelChange={selection.selectModel}
          thinkingLevel={selection.thinkingLevel}
          onThinkingChange={selection.selectThinking}
          modelRequired
          disabled={saving}
        />
      </div>

      <DialogFooter>
        <Button
          type="button"
          variant="ghost"
          disabled={saving}
          onClick={() => onOpenChange(false)}
        >
          {t(($) => $.inspector.cancel)}
        </Button>
        <Button
          type="button"
          disabled={saving || !selection.runtimeId || !selection.model.trim()}
          data-testid="agent-runtime-config-save"
          onClick={() => void handleSave()}
        >
          {saving
            ? t(($) => $.execution_config.dialog_saving)
            : t(($) => $.inspector.save)}
        </Button>
      </DialogFooter>
    </>
  );
}
