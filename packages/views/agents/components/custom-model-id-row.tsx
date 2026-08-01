"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Plus } from "lucide-react";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../i18n";

/**
 * "Custom model ID…" row (Frank 2026-08-01 / Iris spec). Parent only
 * mounts this when the models API reports custom_model_id_supported
 * (backend agent.CustomModelIDSupported — not a frontend whitelist).
 * Click → inline input (not the search box). Enter / confirm submits;
 * Escape / blur cancels. Replaces the old canCreate-from-search path.
 */
export function CustomModelIdRow({
  onSubmit,
  dense = false,
}: {
  onSubmit: (modelId: string) => void;
  /** Inspector picker uses slightly tighter padding/type. */
  dense?: boolean;
}) {
  const { t } = useT("agents");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!editing) return;
    inputRef.current?.focus();
  }, [editing]);

  const commit = () => {
    const next = draft.trim();
    if (!next) {
      setEditing(false);
      setDraft("");
      return;
    }
    onSubmit(next);
    setEditing(false);
    setDraft("");
  };

  const cancel = () => {
    setEditing(false);
    setDraft("");
  };

  if (editing) {
    return (
      <div
        className="flex items-center gap-1.5 border-t border-border px-2 py-1.5"
      >
        <Input
          ref={inputRef}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={t(($) => $.pickers.model_custom_input_placeholder)}
          className={dense ? "h-7 text-xs" : "h-8"}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              commit();
            } else if (e.key === "Escape") {
              e.preventDefault();
              cancel();
            }
          }}
          onBlur={cancel}
        />
        <button
          type="button"
          // Keep focus on the input so blur→cancel doesn't race the click.
          onMouseDown={(e) => e.preventDefault()}
          onClick={commit}
          disabled={!draft.trim()}
          className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-primary transition-colors hover:bg-accent disabled:opacity-40"
          aria-label={t(($) => $.pickers.model_custom_confirm)}
          title={t(($) => $.pickers.model_custom_confirm)}
        >
          <Check className="h-3.5 w-3.5" />
        </button>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={() => setEditing(true)}
      className={`mt-1 flex w-full items-center gap-2 border-t border-border text-left text-primary transition-colors hover:bg-accent/50 ${
        dense ? "px-3 py-2 text-xs" : "px-3 py-2 text-sm"
      }`}
    >
      <Plus className={dense ? "h-3.5 w-3.5 shrink-0" : "h-4 w-4 shrink-0"} />
      <span className="truncate">{t(($) => $.pickers.model_custom_row)}</span>
    </button>
  );
}
