"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import { Input } from "@multica/ui/components/ui/input";
import {
  PickerItem,
  PropertyPicker,
} from "../../../issues/components/pickers";
import { CustomModelIdRow } from "../custom-model-id-row";
import { CHIP_CLASS } from "./chip";
import { EditPencil } from "./inspector-field";
import { useT } from "../../../i18n";

/**
 * Inline model picker for the agent inspector. Lighter cousin of
 * `ModelDropdown` (which is used in the create-agent dialog) — same data
 * source via `runtimeModelsOptions`, but renders inside a PropertyPicker so
 * it fits a single PropRow. Drops the "select a runtime first" state because
 * the inspector only renders this picker after a runtime is bound.
 *
 * Providers whose runtime ignores per-agent model selection report
 * `supported=false` and render an inert italic "Managed by runtime" label
 * instead of a clickable picker. No built-in provider sets this today
 * (Antigravity gained `--model` in agy 1.0.6), but the branch stays for any
 * future model-less runtime.
 */
export function ModelPicker({
  runtimeId,
  value,
  canEdit = true,
  onChange,
}: {
  runtimeId: string | null;
  value: string;
  /** When false, render a static read-only display and skip the popover. */
  canEdit?: boolean;
  onChange: (next: string) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");

  const {
    data: catalog,
    isLoading: catalogLoading,
    isSuccess: catalogSuccess,
  } = useQuery(runtimeModelsOptions(runtimeId));
  const supported = catalog?.supported ?? true;
  // Backend-owned capability — never infer from a frontend provider list.
  const customModelIdSupported =
    catalog?.customModelIdSupported === true;
  // Memoise the model list so every downstream useMemo gets a stable
  // reference; `?? []` would mint a fresh array on every render and
  // invalidate filters needlessly.
  const models = useMemo(
    () => catalog?.models ?? [],
    [catalog],
  );

  const filtered = useMemo(() => {
    const s = search.trim().toLowerCase();
    if (!s) return models;
    return models.filter(
      (m) =>
        m.id.toLowerCase().includes(s) || m.label.toLowerCase().includes(s),
    );
  }, [models, search]);

  const triggerLabel = value || t(($) => $.pickers.model_default);
  const triggerTitle = t(($) => $.pickers.model_tooltip, { value: triggerLabel });

  const select = async (id: string) => {
    setOpen(false);
    setSearch("");
    if (id !== value) await onChange(id);
  };

  if (!supported && !catalogLoading) {
    return (
      <span className="truncate italic text-muted-foreground">
        {t(($) => $.pickers.model_managed_by_runtime)}
      </span>
    );
  }

  if (!canEdit) {
    return (
      <Tooltip>
        <TooltipTrigger
          render={<span className="min-w-0 truncate px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground" />}
        >
          {triggerLabel}
        </TooltipTrigger>
        <TooltipContent side="top">{triggerTitle}</TooltipContent>
      </Tooltip>
    );
  }

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-auto min-w-[16rem] max-w-md"
      align="start"
      tooltip={triggerTitle}
      triggerRender={
        <button
          type="button"
          className={CHIP_CLASS}
          aria-label={triggerTitle}
        />
      }
      trigger={
        <>
          <span className="min-w-0 truncate font-mono text-[11px]">
            {triggerLabel}
          </span>
          <EditPencil />
        </>
      }
      header={
        <div className="p-1.5">
          <Input
            autoFocus
            placeholder={t(($) => $.pickers.model_search_placeholder)}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-7 text-xs"
          />
        </div>
      }
    >
      {catalogLoading && (
        <div className="flex items-center gap-2 p-3 text-xs text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" />
          {t(($) => $.pickers.model_discovering)}
        </div>
      )}

      {!catalogLoading &&
        filtered.map((m) => (
          <PickerItem
            key={m.id}
            selected={m.id === value}
            onClick={() => void select(m.id)}
            // Tooltip carries the canonical model id even when the chip
            // shows the friendlier label, so users can always see what
            // string actually ships to the agent.
            tooltip={m.label !== m.id ? `${m.label} · ${m.id}` : m.id}
          >
            {/* PickerItem wraps children in a flex `<span>`. Putting a
                `<div>` inside that <span> is block-in-inline (invalid
                HTML5) and triggers the browser-default centering quirk
                that pushes descendants off-axis (model IDs floated to the
                center instead of left-aligning under their labels). Use
                `<span block text-left>` to keep layout deterministic —
                matches the fix already applied in thinking-picker.tsx. */}
            <span className="block min-w-0 flex-1 text-left">
              <span className="block truncate text-[13px] font-medium">{m.label}</span>
              {m.label !== m.id && (
                <span className="mt-0.5 block truncate font-mono text-[10px] leading-snug text-muted-foreground">
                  {m.id}
                </span>
              )}
            </span>
          </PickerItem>
        ))}

      {catalogSuccess && filtered.length === 0 && (
        <p className="px-3 py-3 text-center text-xs text-muted-foreground">
          {customModelIdSupported
            ? t(($) => $.pickers.model_empty_custom_hint)
            : t(($) => $.pickers.model_empty)}
        </p>
      )}

      {catalogSuccess && customModelIdSupported && (
        <CustomModelIdRow dense onSubmit={(id) => void select(id)} />
      )}

      {value && (
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
                onClick={() => void select("")}
                className="flex w-full items-center border-t px-3 py-2 text-left text-xs text-muted-foreground transition-colors hover:bg-accent/50"
              >
                {t(($) => $.pickers.model_clear)}
              </button>
            }
          />
          <TooltipContent side="top">{t(($) => $.pickers.model_clear_title)}</TooltipContent>
        </Tooltip>
      )}
    </PropertyPicker>
  );
}
