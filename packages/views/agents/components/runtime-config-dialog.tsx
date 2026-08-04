"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import type {
  Agent,
  AgentRuntime,
  MemberWithUser,
  RuntimeModel,
} from "@multica/core/types";
import { deriveRuntimeHealth, runtimeModelsOptions } from "@multica/core/runtimes";
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
import { ModelPicker } from "./inspector/model-picker";
import { RuntimePicker } from "./inspector/runtime-picker";
import { ThinkingPicker } from "./inspector/thinking-picker";

type Draft = {
  runtime_id: string;
  model: string;
  thinking_level: string;
};

function seedDraft(agent: Agent): Draft {
  return {
    runtime_id: agent.runtime_id,
    model: agent.model ?? "",
    thinking_level: agent.thinking_level ?? "",
  };
}

function pickModelEntry(
  models: RuntimeModel[],
  model: string,
): RuntimeModel | undefined {
  if (model) return models.find((m) => m.id === model);
  return models.find((m) => m.default) ?? models[0];
}

/**
 * LRM-1351 — batch-edit runtime / model / thinking in one centered Dialog.
 * Draft edits stay local; Save issues a single `onSave` patch (at most one
 * restart via `useUpdateAgent`). Cancel / Esc / mask discard the draft.
 */
export function RuntimeConfigDialog({
  agent,
  open,
  onOpenChange,
  runtimes,
  members,
  currentUserId,
  runtimeOnline,
  onSave,
}: {
  agent: Agent;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  runtimeOnline: boolean;
  onSave: (patch: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const [draft, setDraft] = useState<Draft>(() => seedDraft(agent));
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setDraft(seedDraft(agent));
  }, [
    open,
    agent.id,
    agent.runtime_id,
    agent.model,
    agent.thinking_level,
  ]);

  const draftRuntime =
    runtimes.find((r) => r.id === draft.runtime_id) ?? null;
  const draftOnline =
    !!draftRuntime &&
    deriveRuntimeHealth(draftRuntime, Date.now()) === "online";
  // Prefer live draft health; fall back to the caller's summary-row signal
  // when the draft runtime is missing from the list (orphan recovery).
  const modelsOnline =
    draft.runtime_id === agent.runtime_id ? runtimeOnline || draftOnline : draftOnline;

  const { data: modelsData } = useQuery(
    runtimeModelsOptions(modelsOnline ? draft.runtime_id : null),
  );
  const models = modelsData?.models ?? [];
  const thinkingDiscovery = modelsData?.thinkingDiscovery === true;
  const entry = pickModelEntry(models, draft.model);
  const levels = entry?.thinking?.supported_levels ?? [];
  const showThinking =
    !!draft.thinking_level || (thinkingDiscovery && levels.length > 0);

  const dirty = useMemo(() => {
    const base = seedDraft(agent);
    return (
      draft.runtime_id !== base.runtime_id ||
      draft.model !== base.model ||
      draft.thinking_level !== base.thinking_level
    );
  }, [agent, draft]);

  const handleOpenChange = (next: boolean) => {
    if (saving) return;
    if (!next) setDraft(seedDraft(agent));
    onOpenChange(next);
  };

  const handleSave = async () => {
    if (!dirty) {
      handleOpenChange(false);
      return;
    }
    const base = seedDraft(agent);
    const patch: Record<string, unknown> = {};
    if (draft.runtime_id !== base.runtime_id) {
      patch.runtime_id = draft.runtime_id;
    }
    if (draft.model !== base.model) {
      patch.model = draft.model;
    }
    if (draft.thinking_level !== base.thinking_level) {
      patch.thinking_level = draft.thinking_level;
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
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="max-w-md"
        data-testid="agent-runtime-config-dialog"
      >
        <DialogHeader>
          <DialogTitle className="text-sm">
            {t(($) => $.execution_config.dialog_title)}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {t(($) => $.execution_config.dialog_description)}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3 py-1">
          <Field label={t(($) => $.inspector.prop_runtime)}>
            <RuntimePicker
              value={draft.runtime_id}
              boundRuntimeId={agent.runtime_id}
              runtimes={runtimes}
              members={members}
              currentUserId={currentUserId}
              canEdit
              onChange={(id) =>
                setDraft((prev) => ({ ...prev, runtime_id: id }))
              }
            />
          </Field>
          <Field label={t(($) => $.inspector.prop_model)}>
            <ModelPicker
              runtimeId={draft.runtime_id}
              runtimeOnline={modelsOnline && !!draft.runtime_id}
              value={draft.model}
              canEdit
              onChange={(m) => setDraft((prev) => ({ ...prev, model: m }))}
            />
          </Field>
          {showThinking ? (
            <Field label={t(($) => $.inspector.prop_thinking)}>
              <ThinkingPicker
                value={draft.thinking_level}
                levels={levels}
                canEdit
                onChange={(v) =>
                  setDraft((prev) => ({ ...prev, thinking_level: v }))
                }
              />
            </Field>
          ) : null}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            disabled={saving}
            onClick={() => handleOpenChange(false)}
          >
            {t(($) => $.inspector.cancel)}
          </Button>
          <Button
            type="button"
            disabled={saving}
            data-testid="agent-runtime-config-save"
            onClick={() => void handleSave()}
          >
            {saving
              ? t(($) => $.execution_config.dialog_saving)
              : t(($) => $.inspector.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">{children}</div>
    </div>
  );
}
