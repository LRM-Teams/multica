"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Eye,
  EyeOff,
  Loader2,
  Lock,
  Plus,
  Save,
  Trash2,
} from "lucide-react";
import { api } from "@multica/core/api";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../../i18n";

// Env values never reach this component until it fetches them: the resource
// feed no longer carries custom_env at all after MUL-2600. The agent env tab
// uses a reveal-first contract; the runtime env dialog uses `autoReveal`.

let nextEnvId = 0;

interface EnvEntry {
  id: number;
  key: string;
  value: string;
  visible: boolean;
}

function envMapToEntries(env: Record<string, string>): EnvEntry[] {
  return Object.entries(env).map(([key, value]) => ({
    id: nextEnvId++,
    key,
    value,
    visible: false,
  }));
}

function entriesToEnvMap(entries: EnvEntry[]): Record<string, string> {
  const map: Record<string, string> = {};
  for (const entry of entries) {
    const key = entry.key.trim();
    if (key) {
      map[key] = entry.value;
    }
  }
  return map;
}

type EnvPayload = { custom_env: Record<string, string> };

/**
 * Shared custom_env editor. Agent (reveal-first) and runtime (autoReveal +
 * simple) surfaces both reuse this; the caller supplies the reveal / save
 * calls so the component never hardcodes which entity it edits.
 */
export function EnvEditor({
  keyCount,
  getEnv,
  saveEnv,
  onDirtyChange,
  onSaved,
  autoReveal,
  simple,
  onCancel,
}: {
  keyCount?: number;
  getEnv: () => Promise<EnvPayload>;
  saveEnv: (env: Record<string, string>) => Promise<EnvPayload>;
  onDirtyChange?: (dirty: boolean) => void;
  onSaved?: () => void;
  /** Fetch plaintext immediately on mount instead of waiting for a reveal click. */
  autoReveal?: boolean;
  /** Compact mode: no intro copy, plaintext values, no unsaved hint. */
  simple?: boolean;
  /** Rendered as the "cancel" action next to Save in compact mode. */
  onCancel?: () => void;
}) {
  const { t } = useT("agents");

  // revealed === null means "haven't fetched yet"; revealed === [] is
  // a legitimate empty map after a successful fetch.
  const [revealed, setRevealed] = useState<EnvEntry[] | null>(null);
  const [originalMap, setOriginalMap] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState(false);
  const [saving, setSaving] = useState(false);

  const effectiveKeyCount = keyCount ?? 0;

  const currentEnvMap = revealed ? entriesToEnvMap(revealed) : originalMap;
  const dirty =
    revealed !== null &&
    JSON.stringify(currentEnvMap) !== JSON.stringify(originalMap);

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);

  const handleReveal = useCallback(async () => {
    setRevealing(true);
    try {
      const resp = await getEnv();
      const env = resp.custom_env ?? {};
      setOriginalMap(env);
      setRevealed(envMapToEntries(env));
    } catch (err) {
      showErrorToast(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.tab_body.env.reveal_failed_toast),
      );
    } finally {
      setRevealing(false);
    }
  }, [getEnv, t]);

  // autoReveal runs once on mount; the entity id is stable for this
  // component's lifetime (the parent remounts it per entity).
  useEffect(() => {
    if (autoReveal) {
      void handleReveal();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const addEnvEntry = () => {
    setRevealed((prev) => [
      ...(prev ?? []),
      { id: nextEnvId++, key: "", value: "", visible: true },
    ]);
  };

  const removeEnvEntry = (index: number) => {
    setRevealed((prev) => (prev ?? []).filter((_, i) => i !== index));
  };

  const updateEnvEntry = (
    index: number,
    field: "key" | "value",
    val: string,
  ) => {
    setRevealed((prev) =>
      (prev ?? []).map((entry, i) =>
        i === index ? { ...entry, [field]: val } : entry,
      ),
    );
  };

  const toggleEnvVisibility = (index: number) => {
    setRevealed((prev) =>
      (prev ?? []).map((entry, i) =>
        i === index ? { ...entry, visible: !entry.visible } : entry,
      ),
    );
  };

  const handleSave = async () => {
    if (revealed === null) return;
    const keys = revealed.filter((e) => e.key.trim()).map((e) => e.key.trim());
    const uniqueKeys = new Set(keys);
    if (uniqueKeys.size < keys.length) {
      showErrorToast(t(($) => $.tab_body.env.duplicate_keys_toast));
      return;
    }

    setSaving(true);
    try {
      const resp = await saveEnv(currentEnvMap);
      const env = resp.custom_env ?? {};
      setOriginalMap(env);
      setRevealed(envMapToEntries(env));
      toast.success(t(($) => $.tab_body.env.saved_toast));
      onSaved?.();
    } catch (err) {
      showErrorToast(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.tab_body.env.save_failed_toast),
      );
    } finally {
      setSaving(false);
    }
  };

  // autoReveal fetches on mount: show a placeholder list instead of flashing
  // the reveal-first prompt for a request the user never has to make.
  if (revealed === null && autoReveal) {
    return (
      <div
        className="flex items-center justify-center gap-2 rounded-lg border border-dashed py-10 text-xs text-muted-foreground"
        data-testid="env-editor-loading"
      >
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        {t(($) => $.tab_body.env.revealing)}
      </div>
    );
  }

  // Reveal-first pre-fetch state (agent env tab). autoReveal skips this.
  if (revealed === null) {
    return (
      <div className="space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <p className="flex items-center gap-2 text-sm font-medium">
              <Lock className="h-3.5 w-3.5 text-muted-foreground" />
              {effectiveKeyCount > 0
                ? t(($) => $.tab_body.env.not_revealed_title, {
                    count: effectiveKeyCount,
                  })
                : t(($) => $.tab_body.env.not_revealed_empty)}
            </p>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.tab_body.env.not_revealed_hint)}
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={revealing}
            onClick={handleReveal}
            className="shrink-0"
          >
            {revealing ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Eye className="h-3.5 w-3.5" />
            )}
            {revealing
              ? t(($) => $.tab_body.env.revealing)
              : t(($) => $.tab_body.env.reveal_action)}
          </Button>
        </div>
      </div>
    );
  }

  // One shared grid template keeps the header row and every entry row on the
  // same column edges; borderless inputs make the box read as a table.
  const GRID =
    "grid grid-cols-[minmax(0,11rem)_minmax(0,1fr)_auto] items-center gap-2";
  const CELL_INPUT =
    "h-7 rounded-md border-transparent bg-transparent px-1.5 font-mono text-xs dark:bg-transparent";

  const renderValueInput = (entry: EnvEntry, index: number) => {
    if (simple) {
      return (
        <Input
          value={entry.value}
          onChange={(e) => updateEnvEntry(index, "value", e.target.value)}
          placeholder={t(($) => $.tab_body.env.value_placeholder)}
          className={CELL_INPUT}
        />
      );
    }
    return (
      <div className="relative">
        <Input
          type={entry.visible ? "text" : "password"}
          value={entry.value}
          onChange={(e) => updateEnvEntry(index, "value", e.target.value)}
          placeholder={t(($) => $.tab_body.env.value_placeholder)}
          className={cn(CELL_INPUT, "pr-7")}
        />
        <button
          type="button"
          onClick={() => toggleEnvVisibility(index)}
          className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
          aria-label={
            entry.visible
              ? t(($) => $.tab_body.env.hide_value_aria)
              : t(($) => $.tab_body.env.show_value_aria)
          }
        >
          {entry.visible ? (
            <EyeOff className="h-3.5 w-3.5" />
          ) : (
            <Eye className="h-3.5 w-3.5" />
          )}
        </button>
      </div>
    );
  };

  return (
    <div className="space-y-3">
      {!simple ? (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.tab_body.env.intro_prefix)}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
            {"ANTHROPIC_API_KEY"}
          </code>
          {t(($) => $.tab_body.env.intro_separator)}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px]">
            {"ANTHROPIC_BASE_URL"}
          </code>
          {t(($) => $.tab_body.env.intro_suffix)}
        </p>
      ) : null}

      <div className="overflow-hidden rounded-lg border">
        {revealed.length > 0 ? (
          <div className="divide-y">
            {revealed.map((entry, index) => (
              <div key={entry.id} className={cn(GRID, "px-2 py-1")}>
                <Input
                  value={entry.key}
                  onChange={(e) => updateEnvEntry(index, "key", e.target.value)}
                  placeholder={t(($) => $.tab_body.env.key_placeholder)}
                  className={CELL_INPUT}
                />
                {renderValueInput(entry, index)}
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => removeEnvEntry(index)}
                  className="text-muted-foreground hover:text-destructive"
                  aria-label={t(($) => $.tab_body.env.remove_aria)}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>
        ) : (
          <p className="px-4 py-5 text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.env.not_revealed_empty)}
          </p>
        )}
        <button
          type="button"
          onClick={addEnvEntry}
          className="flex w-full items-center justify-center gap-1 border-t bg-muted/30 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <Plus className="h-3 w-3" />
          {t(($) => $.tab_body.common.add)}
        </button>
      </div>

      <div
        className={cn(
          "flex items-center justify-end gap-2",
          simple && "-mx-4 -mb-4 rounded-b-xl border-t bg-muted/50 px-4 py-3",
        )}
      >
        {!simple && dirty ? (
          <span className="mr-auto text-xs text-muted-foreground">
            {t(($) => $.tab_body.common.unsaved_changes)}
          </span>
        ) : null}
        {simple && onCancel ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onCancel}
            disabled={saving}
          >
            {t(($) => $.inspector.cancel)}
          </Button>
        ) : null}
        <Button onClick={handleSave} disabled={!dirty || saving} size="sm">
          {saving ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Save className="h-3.5 w-3.5" />
          )}
          {t(($) => $.tab_body.common.save)}
        </Button>
      </div>
    </div>
  );
}

/**
 * Agent-scoped env editor. Reuses the shared EnvEditor core with the
 * dedicated agent env endpoints and the reveal-first contract.
 */
export function AgentEnvEditor({
  agent,
  onDirtyChange,
  onSaved,
}: {
  agent: Agent;
  onDirtyChange?: (dirty: boolean) => void;
  onSaved?: () => void;
}) {
  return (
    <EnvEditor
      keyCount={agent.custom_env_key_count}
      getEnv={() => api.getAgentEnv(agent.id)}
      saveEnv={(env) => api.updateAgentEnv(agent.id, { custom_env: env })}
      onDirtyChange={onDirtyChange}
      onSaved={onSaved}
    />
  );
}
