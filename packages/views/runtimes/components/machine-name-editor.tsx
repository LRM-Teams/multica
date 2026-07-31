"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2, Pencil } from "lucide-react";
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
  const visibleName = savedName || hostname;
  const isPlaceholder = !savedName;

  useEffect(() => {
    if (editing) {
      setDraft(savedName);
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [editing, savedName]);

  const canEdit = machine.runtimes.length > 0;

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

  if (!canEdit) {
    return (
      <span
        className={cn(
          variant === "title" ? "text-lg font-semibold" : "text-sm font-medium",
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
        className={cn(
          "rounded-md border border-brand bg-background px-2 py-0.5 outline-none",
          variant === "title"
            ? "text-lg font-semibold"
            : variant === "basics"
              ? "text-sm font-medium"
              : "text-sm font-medium",
          className,
        )}
        aria-label={t(($) => $.machine.basics_display_name)}
      />
    );
  }

  const saving = updateRuntime.isPending;

  return (
    <span
      className={cn(
        "group/name inline-flex min-w-0 items-center gap-1.5",
        className,
      )}
    >
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          setEditing(true);
        }}
        aria-label={`${t(($) => $.machine.basics_display_name)}: ${visibleName}`}
        className={cn(
          "inline-flex min-w-0 items-center gap-1.5 text-left",
          variant === "title" ? "text-lg font-semibold" : "text-sm font-medium",
        )}
      >
        <span
          className={cn("truncate", isPlaceholder && "text-muted-foreground")}
          title={visibleName}
        >
          {visibleName}
        </span>
        {saving ? (
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
        ) : (
          <Pencil
            className={cn(
              "h-3 w-3 shrink-0 text-muted-foreground/55",
              variant === "list" && "opacity-55",
            )}
            aria-hidden
          />
        )}
      </button>
    </span>
  );
}
