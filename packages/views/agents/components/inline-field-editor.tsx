"use client";

import { useCallback, useState } from "react";
import { Loader2, Pencil } from "lucide-react";
import { isImeComposing } from "@multica/core/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { CharCounter } from "./char-counter";

/**
 * True in-place field editor (LRM-471): pencil → the value swaps to an
 * input/textarea where it sits — no Popover / Dialog. Used for Profile
 * Display name (single-line) and Description (auto-growing textarea).
 *
 * Keyboard: Enter saves on input; ⌘/Ctrl+Enter on textarea; Esc cancels.
 * Sync `validate` and rejected `onSave` surface inline (LRM-238 — no silent
 * swallow). Draft is seeded in the open handler so the first paint already
 * shows the current value.
 */
export function InlineFieldEditor({
  value,
  onSave,
  kind,
  ariaLabel,
  placeholder,
  emptyLabel,
  validate,
  mapSaveError,
  maxLength,
  readClassName,
  triggerClassName,
  pencilClassName,
  displayValue,
}: {
  value: string;
  onSave: (next: string) => Promise<void>;
  kind: "input" | "textarea";
  ariaLabel: string;
  placeholder?: string;
  emptyLabel?: string;
  validate?: (v: string) => string | null;
  mapSaveError?: (e: unknown) => string | null;
  maxLength?: number;
  readClassName?: string;
  triggerClassName?: string;
  pencilClassName?: string;
  /** Optional override for the read-mode text (e.g. resolved display name). */
  displayValue?: string;
}) {
  const { t } = useT("agents");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const focusOnMount = useCallback(
    (el: HTMLInputElement | HTMLTextAreaElement | null) => el?.focus(),
    [],
  );

  const startEditing = () => {
    setDraft(value);
    setError(null);
    setEditing(true);
  };

  const cancel = () => {
    setEditing(false);
    setError(null);
  };

  const commit = async () => {
    const err = validate?.(draft) ?? null;
    if (err) {
      setError(err);
      return;
    }
    if (maxLength != null && [...draft].length > maxLength) {
      return;
    }
    if (draft === value) {
      setEditing(false);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await onSave(draft);
      setEditing(false);
    } catch (e) {
      const mapped = mapSaveError?.(e);
      setError(
        mapped ??
          (e instanceof Error && e.message
            ? e.message
            : t(($) => $.detail.update_failed_toast)),
      );
    } finally {
      setSaving(false);
    }
  };

  if (editing) {
    const length = [...draft].length;
    const overLimit = maxLength != null && length > maxLength;

    return (
      <div className="space-y-2" data-testid="inline-field-editor">
        {kind === "input" ? (
          <Input
            ref={focusOnMount}
            aria-label={ariaLabel}
            value={draft}
            placeholder={placeholder}
            disabled={saving}
            onChange={(e) => {
              setDraft(e.target.value);
              if (error) setError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.preventDefault();
                cancel();
                return;
              }
              if (isImeComposing(e)) return;
              if (e.key === "Enter") {
                e.preventDefault();
                void commit();
              }
            }}
            className="h-8"
          />
        ) : (
          <Textarea
            ref={focusOnMount}
            aria-label={ariaLabel}
            value={draft}
            placeholder={placeholder}
            disabled={saving}
            onChange={(e) => {
              setDraft(e.target.value);
              if (error) setError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.preventDefault();
                cancel();
                return;
              }
              if (isImeComposing(e)) return;
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                void commit();
              }
            }}
            className="min-h-16 resize-none text-[13px] leading-5 md:text-[13px]"
          />
        )}
        {maxLength != null ? <CharCounter length={length} max={maxLength} /> : null}
        {error ? <p className="text-xs text-destructive">{error}</p> : null}
        <div className="flex items-center justify-end gap-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={cancel}
            disabled={saving}
          >
            {t(($) => $.inspector.cancel)}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={() => void commit()}
            disabled={saving || draft === value || overLimit}
          >
            {saving ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              t(($) => $.inspector.save)
            )}
          </Button>
        </div>
      </div>
    );
  }

  const shown = displayValue ?? value;

  return (
    <button
      type="button"
      onClick={startEditing}
      data-testid="inline-field-editor-trigger"
      className={cn(
        "group -mx-1 inline-flex w-full min-w-0 items-start gap-1.5 rounded px-1 text-left text-[13px] leading-5 transition-colors hover:bg-accent/50",
        triggerClassName,
      )}
    >
      {shown ? (
        <span
          className={cn(
            "min-w-0 flex-1 whitespace-pre-wrap break-words",
            readClassName,
          )}
        >
          {shown}
        </span>
      ) : (
        <span className="min-w-0 flex-1 italic text-muted-foreground/60">
          {emptyLabel ?? placeholder}
        </span>
      )}
      <Pencil
        className={cn(
          "mt-0.5 size-3.5 shrink-0 text-muted-foreground/70 group-hover:text-foreground",
          pencilClassName,
        )}
      />
    </button>
  );
}
