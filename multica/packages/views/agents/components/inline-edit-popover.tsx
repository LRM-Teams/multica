"use client";

import { useCallback, useState, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { isImeComposing } from "@multica/core/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useT } from "../../i18n";

/**
 * Generic single-field popover editor used for the agent display name,
 * description, and `@handle`. Keeps the trigger styling fully in the caller's
 * hands via a render prop.
 *
 * Error handling has two layers:
 *  - `validate` — synchronous, pre-submit grammar/required checks; blocks the
 *    save and shows the returned message inline.
 *  - `mapSaveError` — turns a REJECTED save (thrown by `onSave`) into an inline
 *    message, e.g. a 409 handle-conflict. Returning `null` (the default when
 *    the prop is omitted) preserves the legacy behaviour: the popover stays
 *    open and the parent's own toast surfaces the error.
 *
 * The draft is seeded from `value` in the open handler (not a `useEffect`) so
 * there is never an extra render showing a stale draft between commits.
 */
export function InlineEditPopover({
  value,
  onSave,
  kind,
  title,
  placeholder,
  validate,
  mapSaveError,
  children,
}: {
  value: string;
  onSave: (next: string) => Promise<void>;
  kind: "input" | "textarea";
  title: string;
  placeholder?: string;
  validate?: (v: string) => string | null;
  mapSaveError?: (e: unknown) => string | null;
  children: (triggerProps: {
    onClick: (e: React.MouseEvent) => void;
  }) => ReactNode;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Stable callback ref: focuses the field once when the popover content mounts
  // (equivalent to `autoFocus` but without the attribute, which is jarring for
  // assistive tech and steals focus on some flows).
  const focusOnMount = useCallback(
    (el: HTMLInputElement | HTMLTextAreaElement | null) => el?.focus(),
    [],
  );

  // Seed the draft from the current value each time the popover opens and clear
  // any prior error — no effect, so the first painted frame already shows the
  // fresh value.
  const handleOpenChange = (next: boolean) => {
    if (next) {
      setDraft(value);
      setError(null);
    }
    setOpen(next);
  };

  const commit = async () => {
    const err = validate?.(draft) ?? null;
    if (err) {
      setError(err);
      return;
    }
    if (draft === value) {
      setOpen(false);
      return;
    }
    setSaving(true);
    try {
      await onSave(draft);
      setOpen(false);
    } catch (e) {
      // Surface a mapped inline message (e.g. 409 conflict) when the caller
      // provides one; otherwise defer to the parent's toast.
      setError(mapSaveError?.(e) ?? null);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          children({ onClick: () => handleOpenChange(true) }) as React.ReactElement
        }
      />
      <PopoverContent align="start" className="w-72 p-3">
        <div className="space-y-2">
          <p className="text-xs font-medium">{title}</p>
          {kind === "input" ? (
            <Input
              ref={focusOnMount}
              aria-label={title}
              value={draft}
              onChange={(e) => {
                setDraft(e.target.value);
                if (error) setError(null);
              }}
              placeholder={placeholder}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setOpen(false);
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
              ref={focusOnMount}
              aria-label={title}
              value={draft}
              onChange={(e) => {
                setDraft(e.target.value);
                if (error) setError(null);
              }}
              placeholder={placeholder}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setOpen(false);
                  return;
                }
                if (isImeComposing(e)) return;
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  void commit();
                }
              }}
              rows={3}
              className="w-full resize-none rounded-md border bg-transparent px-2 py-1.5 text-xs outline-none focus-visible:border-input"
            />
          )}
          {error && <p className="text-xs text-destructive">{error}</p>}
          <div className="flex items-center justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setOpen(false)}
              disabled={saving}
            >
              {t(($) => $.inspector.cancel)}
            </Button>
            <Button
              size="sm"
              onClick={() => void commit()}
              disabled={saving || draft === value}
            >
              {saving ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                t(($) => $.inspector.save)
              )}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
