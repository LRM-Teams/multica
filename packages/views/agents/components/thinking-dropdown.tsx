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
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { useState } from "react";
import {
  executionFieldClass,
  executionOptionClass,
  executionOptionSelectedClass,
  executionTriggerClass,
} from "./execution-picker-styles";

export function ThinkingDropdown({
  runtimeId,
  model,
  value,
  onChange,
  disabled,
}: {
  runtimeId: string | null;
  model: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const { data: catalog } = useQuery(runtimeModelsOptions(runtimeId));

  const models = catalog?.models ?? [];
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
    <div className={executionFieldClass}>
      <Label className="text-xs font-medium text-muted-foreground">
        {t(($) => $.create_dialog.thinking_label)}
      </Label>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger disabled={disabled} className={executionTriggerClass}>
          <Brain className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{triggerLabel}</span>
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
              open && "rotate-180",
            )}
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
      className={cn(
        executionOptionClass,
        selected && executionOptionSelectedClass,
      )}
    >
      <span className="min-w-0 flex-1 truncate">
        {label}
        {level?.description ? (
          <span className="text-muted-foreground">
            {" · "}
            {level.description}
          </span>
        ) : null}
      </span>
      {selected ? <Check className="h-3.5 w-3.5 shrink-0 text-primary" /> : null}
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
