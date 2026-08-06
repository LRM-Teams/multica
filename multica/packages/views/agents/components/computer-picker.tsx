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
import { useT } from "../../i18n";

/**
 * Create-flow computer selector (Frank / Parker 2026-08-01): pick the
 * computer first, then a runtime on that computer. Groups runtimes via
 * `buildRuntimeMachines` so the list is one row per machine, not per
 * provider process.
 *
 * Selection seeding stays in the parent (derived effective id) — this
 * component never calls onSelect from an effect.
 */
export function ComputerPicker({
  runtimes,
  runtimesLoading,
  currentUserId,
  selectedMachineId,
  onSelect,
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  currentUserId: string | null;
  selectedMachineId: string;
  onSelect: (machineId: string) => void;
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
    <div className="flex flex-col min-w-0">
      <Label className="text-xs text-muted-foreground">
        {t(($) => $.create_dialog.computer_label)}
      </Label>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={machines.length === 0 && !runtimesLoading}
          className="flex w-full min-w-0 items-center gap-3 rounded-lg border border-border bg-background px-3 py-2.5 mt-1.5 text-left text-sm transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-50"
        >
          {runtimesLoading ? (
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-muted-foreground" />
          ) : (
            <Monitor className="h-4 w-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0 flex-1">
            <div className="truncate font-medium">
              {runtimesLoading
                ? t(($) => $.create_dialog.computer_loading)
                : (selected?.title ?? t(($) => $.create_dialog.computer_none))}
            </div>
            {selected?.subtitle && (
              <div className="truncate text-xs text-muted-foreground">
                {selected.subtitle}
              </div>
            )}
          </div>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${
              open ? "rotate-180" : ""
            }`}
          />
        </PopoverTrigger>
        <PopoverContent
          align="start"
          className="w-[var(--anchor-width)] p-1 max-h-60 overflow-y-auto"
        >
          {machines.length === 0 ? (
            <p className="px-3 py-2 text-xs text-muted-foreground">
              {t(($) => $.create_dialog.computer_none)}
            </p>
          ) : (
            machines.map((machine) => {
              const online = machine.health === "online";
              return (
                <button
                  key={machine.id}
                  type="button"
                  onClick={() => {
                    onSelect(machine.id);
                    setOpen(false);
                  }}
                  className={`flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm transition-colors ${
                    machine.id === selectedMachineId
                      ? "bg-accent"
                      : "hover:bg-accent/50"
                  }`}
                >
                  <Monitor className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-medium">{machine.title}</div>
                    {machine.subtitle && (
                      <div className="mt-0.5 truncate text-xs text-muted-foreground">
                        {machine.subtitle}
                      </div>
                    )}
                  </div>
                  <span
                    className={`h-2 w-2 shrink-0 rounded-full ${
                      online ? "bg-success" : "bg-muted-foreground/40"
                    }`}
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
