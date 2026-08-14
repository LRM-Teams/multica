"use client";

import { useCallback, useLayoutEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Loader2, Pencil } from "lucide-react";
import { isImeComposing } from "@multica/core/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { CharCounter } from "./char-counter";
import { useT } from "../../i18n/use-t";

/**
 * LRM-471 · true in-place field editor (no Popover / Dialog).
 * Pencil → field becomes input (single-line) or auto-growing textarea;
 * Save / Cancel stay inline; Enter (or ⌘/Ctrl+Enter for textarea) commits;
 * Esc cancels; save failures surface inline (LRM-238).
 */
export function InlineFieldEditor({
  value,
  onSave,
  kind,
  label,
  placeholder,
  emptyLabel,
  validate,
  mapSaveError,
  maxLength,
  displayClassName,
  displayContent,
  testId = "inline-field-editor",
}: {
  value: string;
  onSave: (next: string) => Promise<void>;
  kind: "input" | "textarea";
  label: string;
  placeholder?: string;
  emptyLabel?: string;
  validate?: (v: string) => string | null;
  mapSaveError?: (e: unknown) => string | null;
  maxLength?: number;
  displayClassName?: string;
  displayContent?: ReactNode;
  testId?: string;
}) {
  const { t } = useT("agents");
  const [editing, setEditing] = useState(false);
  // Draft is seeded in startEdit (not from props) so react-doctor
  // no-derived-useState stays clean and mid-edit prop updates cannot clobber.
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const textareaFocusedRef = useRef(false);

  const focusOnMount = useCallback(
    (el: HTMLInputElement | HTMLTextAreaElement | null) => {
      if (!el) return;
      el.focus();
      const end = el.value.length;
      el.setSelectionRange(end, end);
    },
    [],
  );

  const setTextareaRef = useCallback(
    (el: HTMLTextAreaElement | null) => {
      textareaRef.current = el;
      if (el && !textareaFocusedRef.current) {
        textareaFocusedRef.current = true;
        focusOnMount(el);
      }
      if (!el) textareaFocusedRef.current = false;
    },
    [focusOnMount],
  );

  const autosize = useCallback(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.max(el.scrollHeight, 40)}px`;
  }, []);

  useLayoutEffect(() => {
    if (editing && kind === "textarea") autosize();
  }, [editing, kind, draft, autosize]);

  const startEdit = () => {
    setDraft(value);
    setError(null);
    textareaFocusedRef.current = false;
    setEditing(true);
  };

  const cancel = () => {
    if (saving) return;
    setEditing(false);
    setError(null);
    setDraft(value);
  };

  const length = [...draft].length;
  const overLimit = maxLength != null && length > maxLength;

  const commit = async () => {
    const err = validate?.(draft) ?? null;
    if (err) {
      setError(err);
      return;
    }
    if (overLimit) return;
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
            : t(($) => $.inspector.save_failed)),
      );
    } finally {
      setSaving(false);
    }
  };

  if (!editing) {
    return (
      <button
        type="button"
        data-testid={`${testId}-trigger`}
        onClick={startEdit}
        className="group -mx-1 inline-flex w-fit max-w-full min-w-0 items-start gap-1.5 rounded px-1 text-left transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {value ? (
          <span
            className={cn(
              "min-w-0 flex-1 whitespace-pre-wrap break-words",
              displayClassName,
            )}
          >
            {displayContent ?? value}
          </span>
        ) : (
          <span className="min-w-0 flex-1 italic text-muted-foreground/60">
            {emptyLabel ?? placeholder ?? ""}
          </span>
        )}
        <Pencil
          className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/70 group-hover:text-foreground"
          aria-hidden
        />
      </button>
    );
  }

  return (
    <div className="space-y-2" data-testid={testId} data-kind={kind}>
      {kind === "input" ? (
        <Input
          ref={focusOnMount}
          aria-label={label}
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
        <textarea
          ref={setTextareaRef}
          aria-label={label}
          value={draft}
          placeholder={placeholder}
          disabled={saving}
          rows={2}
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
          className="w-full resize-none overflow-hidden rounded-md border border-input bg-background px-3 py-2 text-[13px] leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
      )}
      {maxLength != null ? <CharCounter length={length} max={maxLength} /> : null}
      {error ? (
        <p className="text-xs text-destructive" data-testid={`${testId}-error`} role="alert">
          {error}
        </p>
      ) : null}
      <div className="flex items-center justify-end gap-2">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={cancel}
          disabled={saving}
          data-testid={`${testId}-cancel`}
        >
          {t(($) => $.inspector.cancel)}
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={() => void commit()}
          disabled={saving || overLimit || draft === value}
          data-testid={`${testId}-save`}
        >
          {saving ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
          ) : (
            t(($) => $.inspector.save)
          )}
        </Button>
      </div>
    </div>
  );
}
