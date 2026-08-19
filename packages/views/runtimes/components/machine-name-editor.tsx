"use client";

import { useCallback, useRef, useState, type MouseEvent } from "react";
import { Loader2, Pencil } from "lucide-react";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useUpdateRuntime } from "@multica/core/runtimes/mutations";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { machineHostname, type RuntimeMachine } from "./runtime-machines";

type EditorVariant = "list" | "title" | "basics";

interface MachineNameEditorProps {
  machine: RuntimeMachine;
  wsId: string;
  variant?: EditorVariant;
  className?: string;
}

function currentDisplayName(machine: RuntimeMachine): string {
  for (const runtime of machine.runtimes) {
    const value = runtime.display_name?.trim();
    if (value) return value;
  }
  return "";
}

export function MachineNameEditor({
  machine,
  wsId,
  variant = "list",
  className,
}: MachineNameEditorProps) {
  const { t } = useT("runtimes");
  const updateRuntime = useUpdateRuntime(wsId);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [optimistic, setOptimistic] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const hostname = machineHostname(machine) ?? machine.title;
  const savedName = optimistic ?? currentDisplayName(machine);
  // Cloud computers: prefer the create-time sandbox name (machine.title) over
  // daemon device_name / short daemon id so pending and connected labels match.
  // Regular "your computer" rows keep the previous hostname-first behavior.
  const cloudCreateName =
    !savedName && (machine.pendingCloud || !!machine.sandboxInstanceId)
      ? machine.title.trim() || null
      : null;
  const visibleName = savedName || cloudCreateName || hostname;
  const isPlaceholder = !savedName;
  const canEdit = machine.runtimes.length > 0;
  // List rows stay pure display (row click selects the machine). Detail
  // rename is only on the hero title — a second Basics "Display name" pencil
  // duplicated the same field.
  const editable = variant === "title";

  const beginEdit = useCallback(
    (e?: MouseEvent) => {
      e?.stopPropagation();
      if (!canEdit || updateRuntime.isPending) return;
      setDraft(savedName);
      setEditing(true);
      // Focus after the input mounts (same tick as setState would race).
      queueMicrotask(() => {
        inputRef.current?.focus();
        inputRef.current?.select();
      });
    },
    [canEdit, savedName, updateRuntime.isPending],
  );

  const save = useCallback(() => {
    if (!canEdit || updateRuntime.isPending) return;
    const trimmed = draft.trim();
    // BE clears on blank/whitespace (`" "`), not JSON null (omitempty skips null).
    const patchValue = trimmed;
    if (patchValue === savedName) {
      setEditing(false);
      return;
    }
    const previous = savedName;
    setOptimistic(trimmed);
    setEditing(false);

    const runtimeIds = machine.runtimes.map((r) => r.id);
    let completed = 0;
    let failed = false;

    for (const runtimeId of runtimeIds) {
      updateRuntime.mutate(
        { runtimeId, patch: { display_name: patchValue } },
        {
          onSuccess: () => {
            completed += 1;
            if (completed === runtimeIds.length && !failed) {
              setOptimistic(null);
            }
          },
          onError: () => {
            if (!failed) {
              failed = true;
              setOptimistic(previous);
              showErrorToast(t(($) => $.machine.rename_failed));
            }
          },
        },
      );
    }
  }, [
    canEdit,
    draft,
    machine.runtimes,
    savedName,
    t,
    updateRuntime,
  ]);

  const cancel = useCallback(() => {
    setEditing(false);
    setDraft(savedName);
  }, [savedName]);

  if (!editable) {
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              className={cn(
                "truncate text-sm font-medium",
                isPlaceholder && "text-muted-foreground",
                className,
              )}
            />
          }
        >
          {visibleName}
        </TooltipTrigger>
        <TooltipContent side="top">{visibleName}</TooltipContent>
      </Tooltip>
    );
  }

  if (!canEdit) {
    return (
      <span
        className={cn(
          "truncate font-medium",
          variant === "title" ? "text-lg" : "text-sm",
          className,
        )}
      >
        {visibleName}
      </span>
    );
  }

  if (editing) {
    return (
      <input
        ref={inputRef}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            save();
          } else if (e.key === "Escape") {
            e.preventDefault();
            cancel();
          }
        }}
        placeholder={hostname}
        className={cn(
          "rounded-md border border-brand bg-background px-2 py-0.5 font-medium outline-none",
          variant === "title" ? "text-lg" : "text-sm",
          className,
        )}
        aria-label={t(($) => $.machine.basics_display_name)}
      />
    );
  }

  const saving = updateRuntime.isPending;
  const editAria = t(($) => $.machine.rename_aria, { name: visibleName });

  return (
    <Tooltip>
      <TooltipTrigger
        type="button"
        onClick={beginEdit}
        disabled={saving}
        aria-label={editAria}
        className={cn(
          "group/name inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-md text-left",
          "hover:bg-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          variant === "title" ? "-ml-1 px-1 py-0.5" : "px-0.5 py-0.5",
          className,
        )}
      >
      <span
        className={cn(
          "truncate font-medium",
          variant === "title" ? "text-lg" : "text-sm",
          isPlaceholder && "text-muted-foreground",
        )}
      >
        {visibleName}
      </span>
      {saving ? (
        <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
      ) : (
        <Pencil
          className={cn(
            "shrink-0 text-muted-foreground",
            variant === "title" ? "h-3.5 w-3.5" : "h-3 w-3",
          )}
          aria-hidden
        />
      )}
      </TooltipTrigger>
      <TooltipContent side="top">
        {t(($) => $.machine.rename_hint)}
      </TooltipContent>
    </Tooltip>
  );
}
