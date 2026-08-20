"use client";

import { useMemo, useState } from "react";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { RuntimeConfigFields } from "./runtime-config-fields";
import { useRuntimeConfigSelection } from "./use-runtime-config-selection";

type RuntimeConfigPatch = {
  runtime_id: string;
  model: string;
  thinking_level: string;
};

function commonAgentValue(
  agents: Agent[],
  read: (agent: Agent) => string,
): string {
  const first = agents[0] ? read(agents[0]) : "";
  return agents.every((agent) => read(agent) === first) ? first : "";
}

export function BulkRuntimeConfigDialog({
  agents,
  open,
  onOpenChange,
  runtimes,
  members,
  currentUserId,
  onSave,
}: {
  agents: Agent[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  onSave: (patch: RuntimeConfigPatch) => Promise<void>;
}) {
  const { t } = useT("runtimes");
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
        className="max-w-md overflow-hidden"
        data-testid="agent-bulk-runtime-config-dialog"
      >
        <DialogHeader>
          <DialogTitle>
            {t(($) => $.machine.agents_bulk_config_title, {
              count: agents.length,
            })}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.machine.agents_bulk_config_description)}
          </DialogDescription>
        </DialogHeader>

        {open ? (
          <BulkRuntimeConfigDialogBody
            key={agents
              .map(
                (agent) =>
                  `${agent.id}:${agent.runtime_id}:${agent.model ?? ""}:${agent.thinking_level ?? ""}`,
              )
              .join("|")}
            agents={agents}
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

function BulkRuntimeConfigDialogBody({
  agents,
  runtimes,
  members,
  currentUserId,
  saving,
  setSaving,
  onOpenChange,
  onSave,
}: {
  agents: Agent[];
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  saving: boolean;
  setSaving: (saving: boolean) => void;
  onOpenChange: (open: boolean) => void;
  onSave: (patch: RuntimeConfigPatch) => Promise<void>;
}) {
  const { t } = useT("runtimes");
  const initialRuntimeId = commonAgentValue(
    agents,
    (agent) => agent.runtime_id,
  );
  const initialModel = commonAgentValue(
    agents,
    (agent) => agent.model ?? "",
  );
  const initialThinkingLevel = commonAgentValue(
    agents,
    (agent) => agent.thinking_level ?? "",
  );
  const selection = useRuntimeConfigSelection({
    runtimes,
    currentUserId,
    initialRuntimeId,
    initialModel,
    initialThinkingLevel,
    autoSeedMachine: true,
  });

  const dirty = useMemo(
    () =>
      agents.some(
        (agent) =>
          agent.runtime_id !== selection.runtimeId ||
          (agent.model ?? "") !== selection.model ||
          (agent.thinking_level ?? "") !== selection.thinkingLevel,
      ),
    [
      agents,
      selection.model,
      selection.runtimeId,
      selection.thinkingLevel,
    ],
  );

  const handleSave = async () => {
    if (!dirty || !selection.runtimeId || !selection.model.trim()) return;
    setSaving(true);
    try {
      await onSave({
        runtime_id: selection.runtimeId,
        model: selection.model,
        thinking_level: selection.thinkingLevel,
      });
      onOpenChange(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <div className="min-w-0 overflow-y-auto py-1 max-h-[60vh]">
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
          {t(($) => $.machine.agents_cancel)}
        </Button>
        <Button
          type="button"
          disabled={
            saving ||
            !dirty ||
            !selection.runtimeId ||
            !selection.model.trim()
          }
          data-testid="agent-bulk-runtime-config-save"
          onClick={() => void handleSave()}
        >
          {saving
            ? t(($) => $.machine.agents_bulk_config_saving)
            : t(($) => $.machine.agents_bulk_config_save, {
                count: agents.length,
              })}
        </Button>
      </DialogFooter>
    </>
  );
}
