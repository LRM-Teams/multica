"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Cpu, Loader2, Check, Info, RefreshCw } from "lucide-react";
import {
  runtimeModelsKeys,
  runtimeModelsOptions,
} from "@multica/core/runtimes";
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

// ModelDropdown: searchable model picker. A selected runtime always starts
// real catalog discovery; the request result, not cached heartbeat timestamps,
// decides whether discovery succeeded. Freeform model id stays available when
// discovery fails or the catalog is empty —
// CreateAgent already accepts a plain model string without catalog_request_id.
// Providers with supported=false still hide the picker (managed-by-runtime).
export function ModelDropdown({
  runtimeId,
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
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  required?: boolean;
  autoSelectFirst?: boolean;
}) {
  const { t } = useT("agents");
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Query is enabled as soon as runtimeId is set (not on open), so selecting
  // a Runtime starts the daemon list-models scan immediately.
  const {
    data: catalog,
    isLoading: catalogLoading,
    isFetching: catalogFetching,
    isError: catalogError,
  } =
    // react-doctor-disable-next-line react-doctor/no-event-handler -- this hook delivers the external catalog signal reconciled by the explicitly documented effects below.
    useQuery(runtimeModelsOptions(runtimeId));
  // In-flight discovery (initial or user rescan).
  const isFetchingCatalog =
    !!runtimeId &&
    (catalogLoading || catalogFetching);
  // First load only — replace trigger text with the scanning message.
  const isInitialScan = isFetchingCatalog && !catalog;

  const rescanModels = () => {
    if (!runtimeId || isFetchingCatalog || disabled) return;
    void queryClient.invalidateQueries({
      queryKey: runtimeModelsKeys.forRuntime(runtimeId),
    });
  };

  const supported = catalog?.supported ?? true;
  // Backend-owned capability — never infer from a frontend provider list.
  const customModelIdSupported =
    catalog?.customModelIdSupported === true;
  // Stable reference for the model list — `?? []` would mint a fresh
  // array each render and force every downstream useMemo to invalidate.
  const models = useMemo(
    () => catalog?.models ?? [],
    [catalog],
  );
  const grouped = useMemo(() => groupByProvider(models), [models]);

  // No catalog answer → still allow freeform after an error or empty result.
  // Capability false keeps freeform off (provider manages model).
  const catalogSettled = !catalogLoading;
  const freeformAllowed =
    customModelIdSupported ||
    catalogError ||
    (catalogSettled && models.length === 0);

  const catalogHint = catalogError
    ? t(($) => $.model_dropdown.discovery_failed)
    : null;

  // When the selected runtime reports it doesn't support per-agent
  // model selection, clear any previously-saved value so we don't
  // persist a ghost configuration that never takes effect.
  // react-doctor-disable-next-line react-doctor/no-event-handler -- catalog capability is an external async signal; no local event owns this reconciliation.
  useEffect(() => {
    if (!supported && value !== "") {
      onChangeRef.current("");
    }
  }, [supported, value]);

  // Seed a concrete catalog model on create/hire so "Create" does not
  // silently 400 with "model is required" while the trigger still reads
  // like a default is already chosen.
  // react-doctor-disable-next-line react-doctor/no-event-handler -- auto-selection reacts to the external catalog response, not a local event handler.
  useEffect(() => {
    if (!autoSelectFirst || !supported || catalogLoading) return;
    if (value.trim()) return;
    const first = models[0]?.id?.trim();
    if (first) onChangeRef.current(first);
  }, [
    autoSelectFirst,
    supported,
    catalogLoading,
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

  if (!supported && !catalogLoading) {
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

  const canRescan = !!runtimeId && !disabled && !isFetchingCatalog;

  return (
    <div className={executionFieldClass}>
      <div className="flex items-center justify-between gap-2">
        <Label className="text-xs font-medium text-muted-foreground">
          {t(($) => $.model_dropdown.label)}
        </Label>
        <div className="flex min-w-0 items-center gap-1">
          {catalogHint && !isFetchingCatalog ? (
            <span
              className="truncate text-[11px] text-muted-foreground"
              data-testid="model-dropdown-catalog-hint"
            >
              {catalogHint}
            </span>
          ) : null}
          <button
            type="button"
            data-testid="model-dropdown-rescan"
            className={cn(
              "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors",
              "hover:bg-accent hover:text-foreground",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              "disabled:pointer-events-none disabled:opacity-40",
            )}
            disabled={!canRescan}
            aria-label={t(($) => $.model_dropdown.rescan_aria)}
            title={t(($) => $.model_dropdown.rescan_aria)}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              rescanModels();
            }}
          >
            <RefreshCw
              className={cn(
                "h-3.5 w-3.5",
                isFetchingCatalog && "animate-spin",
              )}
              aria-hidden
            />
          </button>
        </div>
      </div>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={disabled}
          data-testid="model-dropdown-trigger"
          className={executionTriggerClass}
          aria-busy={isFetchingCatalog || undefined}
          title={
            !isInitialScan && value
              ? modelLabel(models, value)
              : undefined
          }
        >
          {isInitialScan ? (
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
          ) : (
            <Cpu className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span
            data-testid="model-dropdown-trigger-label"
            className="min-w-0 flex-1 truncate"
          >
            {isInitialScan
              ? t(($) => $.model_dropdown.scanning_on_computer)
              : value
                ? modelLabel(models, value)
                : triggerLabel}
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
            {isInitialScan && (
              <div className="flex items-center gap-2 px-3 py-6 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                {t(($) => $.model_dropdown.scanning_on_computer)}
              </div>
            )}

            {!isInitialScan &&
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

            {!isInitialScan &&
              Object.keys(filtered).length === 0 && (
              <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                {freeformAllowed
                  ? t(($) => $.pickers.model_empty_custom_hint)
                  : t(($) => $.pickers.model_empty_with_dot)}
              </div>
            )}

            {!isInitialScan && freeformAllowed && (
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
      {isFetchingCatalog ? (
        <p
          className="text-[11px] leading-snug text-muted-foreground"
          data-testid="model-dropdown-scanning-hint"
        >
          {t(($) => $.model_dropdown.scanning_on_computer)}
        </p>
      ) : null}
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
  return found?.label ?? id;
}
