"use client";

import { useMemo, useState } from "react";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import {
  RuntimeConfigDialogFooter,
  RuntimeConfigSelectionFields,
} from "./runtime-config-dialog-shared";
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
        <RuntimeConfigSelectionFields
          runtimes={runtimes}
          members={members}
          currentUserId={currentUserId}
          selection={selection}
          disabled={saving}
        />
      </div>

      <RuntimeConfigDialogFooter
        saving={saving}
        disabled={!dirty || !selection.runtimeId || !selection.model.trim()}
        cancelLabel={t(($) => $.machine.agents_cancel)}
        saveLabel={
          saving
            ? t(($) => $.machine.agents_bulk_config_saving)
            : t(($) => $.machine.agents_bulk_config_save, {
                count: agents.length,
              })
        }
        saveTestId="agent-bulk-runtime-config-save"
        onCancel={() => onOpenChange(false)}
        onSave={() => void handleSave()}
      />
    </>
  );
}
