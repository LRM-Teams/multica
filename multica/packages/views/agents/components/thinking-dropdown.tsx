"use client";

import { useQuery } from "@tanstack/react-query";
import { Brain, ChevronDown, Check } from "lucide-react";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import type { RuntimeModel, RuntimeModelThinkingLevel } from "@multica/core/types";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";
import { useState } from "react";

export function ThinkingDropdown({
  runtimeId,
  runtimeOnline,
  model,
  value,
  onChange,
  disabled,
}: {
  runtimeId: string | null;
  runtimeOnline: boolean;
  model: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const modelsQuery = useQuery(
    runtimeModelsOptions(runtimeOnline ? runtimeId : null),
  );

  const models = modelsQuery.data?.models ?? [];
  const entry = pickModelEntry(models, model);
  const levels = entry?.thinking?.supported_levels ?? [];
  if (levels.length === 0 && !value) return null;

  const selected = value ? levels.find((l) => l.value === value) : undefined;
  const triggerLabel = selected
    ? selected.label
    : value || t(($) => $.pickers.thinking_default);

  const select = (next: string) => {
    onChange(next);
    setOpen(false);
  };

  return (
    <div className="flex min-w-0 flex-col">
      <div className="flex h-6 items-center">
        <Label className="text-xs text-muted-foreground">
          {t(($) => $.create_dialog.thinking_label)}
        </Label>
      </div>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={disabled}
          className="mt-1.5 flex w-full min-w-0 items-center gap-3 rounded-lg border border-border bg-background px-3 py-2.5 text-left text-sm transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-50"
        >
          <Brain className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate font-medium">{triggerLabel}</span>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
          />
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[var(--anchor-width)] p-1">
          <ThinkingRow
            label={t(($) => $.pickers.thinking_default)}
            selected={value === ""}
            onClick={() => select("")}
          />
          {levels.map((level) => (
            <ThinkingRow
              key={level.value}
              level={level}
              label={level.label}
              selected={level.value === value}
              onClick={() => select(level.value)}
            />
          ))}
        </PopoverContent>
      </Popover>
    </div>
  );
}

function ThinkingRow({
  label,
  level,
  selected,
  onClick,
}: {
  label: string;
  level?: RuntimeModelThinkingLevel;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors ${
        selected ? "bg-accent" : "hover:bg-accent/50"
      }`}
    >
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{label}</span>
        {level?.description && (
          <span className="mt-0.5 block text-xs leading-snug text-muted-foreground">
            {level.description}
          </span>
        )}
      </span>
      {selected && <Check className="h-4 w-4 shrink-0 text-primary" />}
    </button>
  );
}

function pickModelEntry(models: RuntimeModel[], model: string): RuntimeModel | undefined {
  if (model) return models.find((m) => m.id === model);
  return (
    models.find((m) => m.default) ??
    models.find((m) => (m.thinking?.supported_levels.length ?? 0) > 0) ??
    models[0]
  );
}
