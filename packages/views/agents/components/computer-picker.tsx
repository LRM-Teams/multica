"use client";

import { useMemo, useState } from "react";
import { ChevronDown, Loader2, Monitor } from "lucide-react";
import type { RuntimeDevice } from "@multica/core/types";
import { buildRuntimeMachines } from "../../runtimes/components/runtime-machines";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  runtimeConfigFieldClass,
  runtimeConfigOptionClass,
  runtimeConfigOptionSelectedClass,
  runtimeConfigTriggerClass,
} from "./runtime-config-picker-styles";

/**
 * Computer selector: one row per machine (via `buildRuntimeMachines`).
 * Trigger is single-line Input density; secondary detail stays in the menu.
 */
export function ComputerPicker({
  runtimes,
  runtimesLoading,
  currentUserId,
  selectedMachineId,
  onSelect,
  disabled = false,
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  currentUserId: string | null;
  selectedMachineId: string;
  onSelect: (machineId: string) => void;
  disabled?: boolean;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);

  const machines = useMemo(
    () => buildRuntimeMachines(runtimes, { now: Date.now(), currentUserId }),
    [runtimes, currentUserId],
  );

  const selected =
    machines.find((machine) => machine.id === selectedMachineId) ?? null;

  return (
    <div className={runtimeConfigFieldClass}>
      <Label className="text-xs font-medium text-muted-foreground">
        {t(($) => $.create_dialog.computer_label)}
      </Label>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={disabled || (machines.length === 0 && !runtimesLoading)}
          data-testid="computer-picker-trigger"
          className={runtimeConfigTriggerClass}
        >
          {runtimesLoading ? (
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
          ) : (
            <Monitor className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 flex-1 truncate">
            {runtimesLoading
              ? t(($) => $.create_dialog.computer_loading)
              : (selected?.title ?? t(($) => $.create_dialog.computer_none))}
          </span>
          {selected ? (
            <span
              className={cn(
                "h-1.5 w-1.5 shrink-0 rounded-full",
                selected.health === "online"
                  ? "bg-success"
                  : "bg-muted-foreground/40",
              )}
            />
          ) : null}
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
              open && "rotate-180",
            )}
          />
        </PopoverTrigger>
        <PopoverContent
          align="start"
          className="w-[var(--anchor-width)] max-h-60 overflow-y-auto p-1"
        >
          {machines.length === 0 ? (
            <p className="px-2.5 py-1.5 text-xs text-muted-foreground">
              {t(($) => $.create_dialog.computer_none)}
            </p>
          ) : (
            machines.map((machine) => {
              const online = machine.health === "online";
              return (
                <button
                  key={machine.id}
                  type="button"
                  data-testid={`computer-picker-option-${machine.id}`}
                  onClick={() => {
                    onSelect(machine.id);
                    setOpen(false);
                  }}
                  className={cn(
                    runtimeConfigOptionClass,
                    machine.id === selectedMachineId &&
                      runtimeConfigOptionSelectedClass,
                  )}
                >
                  <Monitor className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">
                    {machine.title}
                    {machine.subtitle ? (
                      <span className="text-muted-foreground">
                        {" · "}
                        {machine.subtitle}
                      </span>
                    ) : null}
                  </span>
                  <span
                    className={cn(
                      "h-1.5 w-1.5 shrink-0 rounded-full",
                      online ? "bg-success" : "bg-muted-foreground/40",
                    )}
                    aria-label={
                      online
                        ? t(($) => $.inspector.computer_connected)
                        : t(($) => $.inspector.computer_disconnected)
                    }
                  />
                </button>
              );
            })
          )}
        </PopoverContent>
      </Popover>
    </div>
  );
}
