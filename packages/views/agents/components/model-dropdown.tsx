"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, Cpu, Loader2, Check, Info } from "lucide-react";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import type { RuntimeModel } from "@multica/core/types";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import { CustomModelIdRow } from "./custom-model-id-row";
import {
  executionFieldClass,
  executionTriggerClass,
} from "./execution-picker-styles";
import { useT } from "../../i18n";

// ModelDropdown: searchable model picker. Catalog fetch is gated on
// `runtimeOnline` only (#124 / Frank+Parker 2026-08-04). Freeform model id
// stays available when offline, discovery fails, or the catalog is empty —
// CreateAgent already accepts a plain model string without catalog_request_id.
// Providers with supported=false still hide the picker (managed-by-runtime).
export function ModelDropdown({
  runtimeId,
  runtimeOnline,
  value,
  onChange,
  disabled,
  // Create/hire flows (LRM-808): empty model is rejected by the API. Prefer
  // an explicit pick (and optionally seed the first catalog entry) instead of
  // showing a fake "provider default" that submits blank.
  required = false,
  autoSelectFirst = false,
}: {
  runtimeId: string | null;
  runtimeOnline: boolean;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  required?: boolean;
  autoSelectFirst?: boolean;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Catalog only while the runtime is online — never disable the control.
  const modelsQuery = useQuery(
    runtimeModelsOptions(runtimeOnline ? runtimeId : null),
  );

  const supported = modelsQuery.data?.supported ?? true;
  // Backend-owned capability — never infer from a frontend provider list.
  const customModelIdSupported =
    modelsQuery.data?.customModelIdSupported === true;
  // Stable reference for the model list — `?? []` would mint a fresh
  // array each render and force every downstream useMemo to invalidate.
  const models = useMemo(
    () => modelsQuery.data?.models ?? [],
    [modelsQuery.data],
  );
  const grouped = useMemo(() => groupByProvider(models), [models]);

  // No live catalog answer → still allow freeform (offline / error / empty).
  // Online + capability false → keep freeform off (provider manages model).
  const catalogSettled = !runtimeOnline || !modelsQuery.isLoading;
  const freeformAllowed =
    customModelIdSupported ||
    !runtimeOnline ||
    modelsQuery.isError ||
    (catalogSettled && models.length === 0);

  const catalogHint =
    !runtimeOnline
      ? t(($) => $.model_dropdown.catalog_unavailable_hint)
      : modelsQuery.isError
        ? t(($) => $.model_dropdown.discovery_failed)
        : null;

  // When the selected runtime reports it doesn't support per-agent
  // model selection, clear any previously-saved value so we don't
  // persist a ghost configuration that never takes effect.
  useEffect(() => {
    if (!supported && value !== "") {
      onChangeRef.current("");
    }
  }, [supported, value]);

  // Seed a concrete catalog model on create/hire so "Create" does not
  // silently 400 with "model is required" while the trigger still reads
  // like a default is already chosen. Skip when offline (no catalog).
  useEffect(() => {
    if (!autoSelectFirst || !supported || !runtimeOnline || modelsQuery.isLoading)
      return;
    if (value.trim()) return;
    const first = models[0]?.id?.trim();
    if (first) onChangeRef.current(first);
  }, [
    autoSelectFirst,
    supported,
    runtimeOnline,
    modelsQuery.isLoading,
    models,
    value,
  ]);

  const filtered = useMemo(() => {
    if (!search.trim()) return grouped;
    const needle = search.toLowerCase();
    const out: Record<string, RuntimeModel[]> = {};
    for (const [provider, list] of Object.entries(grouped)) {
      const matches = list.filter(
        (m) =>
          m.id.toLowerCase().includes(needle) ||
          m.label.toLowerCase().includes(needle),
      );
      if (matches.length > 0) out[provider] = matches;
    }
    return out;
  }, [grouped, search]);

  const select = (id: string) => {
    onChange(id);
    setOpen(false);
    setSearch("");
  };

  // Empty trigger: never claim "runtime offline" as if model is broken —
  // prefer select/required/default copy; soft catalog hint sits beside label.
  const triggerLabel =
    value ||
    (disabled
      ? t(($) => $.model_dropdown.select_runtime_first)
      : required
        ? t(($) => $.model_dropdown.select_required)
        : t(($) => $.model_dropdown.default_provider));

  if (!supported && !modelsQuery.isLoading) {
    return (
      <div className={executionFieldClass}>
        <Label className="text-xs font-medium text-muted-foreground">
          {t(($) => $.model_dropdown.label)}
        </Label>
        <div className="flex h-8 items-center gap-2 rounded-lg border border-dashed border-border bg-muted/30 px-2.5 text-xs text-muted-foreground">
          <Info className="h-3.5 w-3.5 shrink-0" />
          <span className="min-w-0 truncate">
            {t(($) => $.model_dropdown.managed_by_runtime_title)}
          </span>
        </div>
      </div>
    );
  }

  return (
    <div className={executionFieldClass}>
      <div className="flex items-center justify-between gap-2">
        <Label className="text-xs font-medium text-muted-foreground">
          {t(($) => $.model_dropdown.label)}
        </Label>
        {catalogHint ? (
          <span
            className="truncate text-[11px] text-muted-foreground"
            data-testid="model-dropdown-catalog-hint"
          >
            {catalogHint}
          </span>
        ) : null}
      </div>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={disabled}
          data-testid="model-dropdown-trigger"
          className={executionTriggerClass}
        >
          <Cpu className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">
            {value ? modelLabel(models, value) || value : triggerLabel}
          </span>
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
              open && "rotate-180",
            )}
          />
        </PopoverTrigger>
        <PopoverContent
          align="start"
          className="w-[var(--anchor-width)] p-0 overflow-hidden"
        >
          <div className="border-b border-border p-2">
            <Input
              autoFocus
              placeholder={t(($) => $.pickers.model_search_placeholder)}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="h-8"
            />
          </div>
          <div className="max-h-72 overflow-y-auto p-1">
            {runtimeOnline && modelsQuery.isLoading && (
              <div className="flex items-center gap-2 px-3 py-6 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                {t(($) => $.pickers.model_discovering)}
              </div>
            )}

            {(!runtimeOnline || !modelsQuery.isLoading) &&
              Object.entries(filtered).map(([provider, list]) => (
                <div key={provider} className="mb-1">
                  {provider && (
                    <div className="px-2 pt-1.5 pb-0.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                      {provider}
                    </div>
                  )}
                  {list.map((m) => (
                    <button
                      type="button"
                      key={m.id}
                      onClick={() => select(m.id)}
                      className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors ${
                        m.id === value ? "bg-accent" : "hover:bg-accent/50"
                      }`}
                    >
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-medium">{m.label}</div>
                        {m.label !== m.id && (
                          <div className="truncate text-xs text-muted-foreground">
                            {m.id}
                          </div>
                        )}
                      </div>
                      {m.id === value && (
                        <Check className="h-4 w-4 shrink-0 text-primary" />
                      )}
                    </button>
                  ))}
                </div>
              ))}

            {(!runtimeOnline || !modelsQuery.isLoading) &&
              Object.keys(filtered).length === 0 && (
              <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                {freeformAllowed
                  ? t(($) => $.pickers.model_empty_custom_hint)
                  : t(($) => $.pickers.model_empty_with_dot)}
              </div>
            )}

            {(!runtimeOnline || !modelsQuery.isLoading) && freeformAllowed && (
              <CustomModelIdRow onSubmit={select} />
            )}

            {value && !required && (
              <button
                type="button"
                onClick={() => select("")}
                className="flex w-full items-center gap-2 border-t border-border px-3 py-2 text-left text-xs text-muted-foreground transition-colors hover:bg-accent/50"
              >
                {t(($) => $.model_dropdown.clear_full)}
              </button>
            )}
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}

function groupByProvider(models: RuntimeModel[]): Record<string, RuntimeModel[]> {
  const out: Record<string, RuntimeModel[]> = {};
  for (const m of models) {
    const key = m.provider ?? "";
    if (!out[key]) out[key] = [];
    out[key].push(m);
  }
  return out;
}

function modelLabel(models: RuntimeModel[], id: string): string {
  const found = models.find((m) => m.id === id);
  if (!found) return "custom";
  return found.provider ? found.provider : "model";
}
