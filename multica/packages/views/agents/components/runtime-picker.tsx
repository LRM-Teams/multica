"use client";

import { useEffect, useMemo, useState } from "react";
import { ChevronDown, Cloud, Loader2 } from "lucide-react";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import { runtimeDisplayLabel } from "../../runtimes/components/runtime-machines";
import { ActorAvatar } from "../../common/actor-avatar";
import { deriveRuntimeHealth } from "@multica/core/runtimes";
import type { MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { resolveActorDisplayName } from "@multica/core/identity";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";

export function RuntimePicker({
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
  selectedRuntimeId,
  onSelect,
  /**
   * Create-flow label override. After computer-first selection, the second
   * picker is the code agent on that computer — keep the default "Runtime"
   * label elsewhere so we don't do a product-wide rename.
   */
  label,
  /**
   * Per-row label. Default keeps `runtimeDisplayLabel` (honours machine
   * rename). Code-agent-after-computer passes `runtime.name` so Cursor vs
   * Pi stay distinct when they share the machine `display_name`.
   */
  getItemLabel = runtimeDisplayLabel,
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  members: MemberWithUser[];
  currentUserId: string | null;
  selectedRuntimeId: string;
  onSelect: (id: string) => void;
  label?: string;
  getItemLabel?: (runtime: RuntimeDevice) => string;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const pickerLabel = label ?? t(($) => $.create_dialog.runtime_label);

  const getOwnerMember = (ownerId: string | null) => {
    if (!ownerId) return null;
    return members.find((m) => m.user_id === ownerId) ?? null;
  };

  // Others' private runtimes are excluded outright, not shown-disabled —
  // a private runtime that isn't mine has nothing for me to do with it.
  const visibleRuntimes = useMemo(
    // react-doctor-disable-next-line react-doctor/no-event-handler -- flags the useEffect below that seeds selection from this list; it reacts to `runtimes` arriving from the parent's query/WS subscription, not a local user event this component can hook a handler into.
    () => sortRuntimesForPicker(runtimes, currentUserId),
    [runtimes, currentUserId],
  );

  const selectedRuntime =
    runtimes.find((d) => d.id === selectedRuntimeId) ?? null;

  // Sole source of truth for seeding the parent's selection when it's empty
  // — first mount with no template runtime, or runtimes arriving later over
  // WS. Only fires when `selectedRuntimeId === ""` so a duplicate-mode
  // pre-fill (template runtime) is never silently overwritten.
  useEffect(() => {
    if (selectedRuntimeId !== "") return;
    const firstUsable = visibleRuntimes[0];
    if (firstUsable) onSelect(firstUsable.id);
  }, [visibleRuntimes, selectedRuntimeId, onSelect]);

  // Computed once per render, outside JSX, so react:doctor's
  // hydration-mismatch rule doesn't flag a fresh Date.now() per row.
  const now = Date.now();

  return (
    <div className="flex flex-col min-w-0">
      <div className="flex h-6 items-center justify-between">
        <Label className="text-xs text-muted-foreground">
          {pickerLabel}
        </Label>
      </div>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={runtimes.length === 0 && !runtimesLoading}
          className="flex w-full min-w-0 items-center gap-3 rounded-lg border border-border bg-background px-3 py-2.5 mt-1.5 text-left text-sm transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-50"
        >
          {runtimesLoading ? (
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-muted-foreground" />
          ) : selectedRuntime ? (
            <ProviderLogo
              provider={selectedRuntime.provider}
              className="h-4 w-4 shrink-0"
            />
          ) : (
            <Cloud className="h-4 w-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate font-medium">
                {runtimesLoading
                  ? t(($) => $.create_dialog.runtime_loading)
                  : (selectedRuntime
                      ? getItemLabel(selectedRuntime)
                      : t(($) => $.create_dialog.runtime_none))}
              </span>
              {selectedRuntime?.runtime_mode === "cloud" && (
                <span className="shrink-0 rounded bg-brand/10 px-1.5 py-0.5 text-xs font-medium text-brand">
                  {t(($) => $.create_dialog.runtime_cloud_badge)}
                </span>
              )}
            </div>
            {selectedRuntime && (
              <div className="truncate text-xs text-muted-foreground">
                {getOwnerMember(selectedRuntime.owner_id)?.name ??
                  selectedRuntime.device_info}
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
          {visibleRuntimes.length === 0 ? (
            <p
              className="px-3 py-2.5 text-center text-xs text-muted-foreground"
              data-testid="runtime-picker-empty"
            >
              {t(($) => $.create_dialog.runtime_empty)}
            </p>
          ) : (
            visibleRuntimes.map((device) => {
              const ownerMember = getOwnerMember(device.owner_id);
              return (
                <button
                  key={device.id}
                  type="button"
                  onClick={() => {
                    onSelect(device.id);
                    setOpen(false);
                  }}
                  className={`flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm transition-colors ${
                    device.id === selectedRuntimeId
                      ? "bg-accent"
                      : "hover:bg-accent/50"
                  }`}
                >
                  <ProviderLogo
                    provider={device.provider}
                    className="h-4 w-4 shrink-0"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium">{getItemLabel(device)}</span>
                      {device.runtime_mode === "cloud" && (
                        <span className="shrink-0 rounded bg-brand/10 px-1.5 py-0.5 text-xs font-medium text-brand">
                          {t(($) => $.create_dialog.runtime_cloud_badge)}
                        </span>
                      )}
                    </div>
                    <div className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
                      {ownerMember ? (
                        <>
                          <ActorAvatar
                            actorType="member"
                            actorId={ownerMember.user_id}
                            size={14}
                          />
                          <span className="truncate">
                            {resolveActorDisplayName(ownerMember, ownerMember.user_id)}
                          </span>
                        </>
                      ) : (
                        <span className="truncate">{device.device_info}</span>
                      )}
                    </div>
                  </div>
                  <span
                    className={`h-2 w-2 shrink-0 rounded-full ${
                      deriveRuntimeHealth(device, now) === "online"
                        ? "bg-success"
                        : "bg-muted-foreground/40"
                    }`}
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

// Visibility gate exposed so the parent can defend Create against a locked
// selection (e.g. duplicate of an agent whose runtime is now private).
export function isRuntimeUsableForUser(
  r: RuntimeDevice,
  currentUserId: string | null,
): boolean {
  if (!currentUserId) return true;
  if (r.owner_id === currentUserId) return true;
  return r.visibility === "public";
}

// Others' private runtimes are excluded, not shown-disabled — a private
// runtime that isn't mine and isn't public has nothing for me to do with it.
function sortRuntimesForPicker(
  runtimes: RuntimeDevice[],
  currentUserId: string | null,
): RuntimeDevice[] {
  return runtimes
    .filter((r) => isRuntimeUsableForUser(r, currentUserId))
    .toSorted((a, b) => {
      const aMine = a.owner_id === currentUserId;
      const bMine = b.owner_id === currentUserId;
      if (aMine && !bMine) return -1;
      if (!aMine && bMine) return 1;
      return 0;
    });
}
