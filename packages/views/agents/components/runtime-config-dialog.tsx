"use client";

import { useMemo, useState } from "react";
import { ChevronDown } from "lucide-react";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@multica/ui/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { AgentEnvEditor } from "./agent-env-editor";
import {
  RuntimeConfigDialogFooter,
  RuntimeConfigSelectionFields,
} from "./runtime-config-dialog-shared";
import { useRuntimeConfigSelection } from "./use-runtime-config-selection";

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
        className="max-w-md overflow-hidden"
        data-testid="agent-runtime-config-dialog"
      >
        <DialogHeader>
          <DialogTitle className="text-sm">
            {t(($) => $.runtime_config.dialog_title)}
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
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const selection = useRuntimeConfigSelection({
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
      <div className="min-w-0 space-y-4 overflow-y-auto py-1 max-h-[60vh]">
        <RuntimeConfigSelectionFields
          runtimes={runtimes}
          members={members}
          currentUserId={currentUserId}
          selection={selection}
          disabled={saving}
        />

        <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
          <CollapsibleTrigger
            type="button"
            className="flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            data-testid="agent-runtime-config-more"
          >
            <ChevronDown
              className={`h-3.5 w-3.5 transition-transform ${advancedOpen ? "rotate-180" : ""}`}
            />
            {t(($) => $.runtime_config.more)}
          </CollapsibleTrigger>
          <CollapsibleContent className="pt-1">
            <div className="space-y-3 rounded-lg border p-3">
              <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                {t(($) => $.runtime_config.advanced)}
              </p>
              <div className="space-y-2">
                <p className="text-sm font-medium">
                  {t(($) => $.runtime_config.env_vars_title)}
                </p>
                <AgentEnvEditor agent={agent} />
              </div>
            </div>
          </CollapsibleContent>
        </Collapsible>
      </div>

      <RuntimeConfigDialogFooter
        saving={saving}
        cancelLabel={t(($) => $.inspector.cancel)}
        saveLabel={
          saving
            ? t(($) => $.runtime_config.dialog_saving)
            : t(($) => $.inspector.save)
        }
        saveTestId="agent-runtime-config-save"
        onCancel={() => onOpenChange(false)}
        onSave={() => void handleSave()}
      />
    </>
  );
}
