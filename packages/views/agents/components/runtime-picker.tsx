"use client";

import { useEffect, useMemo, useState } from "react";
import { ChevronDown, Cloud, Loader2 } from "lucide-react";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import { ActorAvatar } from "../../common/actor-avatar";
import type { MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { resolveActorDisplayName } from "@multica/core/identity";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";
import {
  runtimePickerBrandLabel,
  runtimePickerHostSubtitle,
} from "./runtime-picker-labels";

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
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  members: MemberWithUser[];
  currentUserId: string | null;
  selectedRuntimeId: string;
  onSelect: (id: string) => void;
  label?: string;
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

  const selectedOwner = selectedRuntime
    ? getOwnerMember(selectedRuntime.owner_id)
    : null;
  const selectedIsMine =
    !!selectedRuntime &&
    !!currentUserId &&
    selectedRuntime.owner_id === currentUserId;
  const selectedHost = selectedRuntime
    ? runtimePickerHostSubtitle(selectedRuntime, visibleRuntimes)
    : null;

  return (
    <div className="flex flex-col min-w-0">
      <div className="flex h-6 items-center justify-between">
        <Label className="text-xs text-muted-foreground">{pickerLabel}</Label>
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
                  : selectedRuntime
                    ? runtimePickerBrandLabel(selectedRuntime)
                    : t(($) => $.create_dialog.runtime_none)}
              </span>
              {selectedRuntime?.runtime_mode === "cloud" && (
                <span className="shrink-0 rounded bg-brand/10 px-1.5 py-0.5 text-xs font-medium text-brand">
                  {t(($) => $.create_dialog.runtime_cloud_badge)}
                </span>
              )}
            </div>
            {selectedRuntime && !selectedIsMine && selectedOwner ? (
              <div className="truncate text-xs text-muted-foreground">
                {resolveActorDisplayName(selectedOwner, selectedOwner.user_id)}
              </div>
            ) : selectedHost ? (
              <div className="truncate text-xs text-muted-foreground">
                {selectedHost}
              </div>
            ) : null}
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
              const isMine =
                !!currentUserId && device.owner_id === currentUserId;
              const host = runtimePickerHostSubtitle(device, visibleRuntimes);
              const showOwner = !isMine && !!ownerMember;
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
                      <span className="truncate font-medium">
                        {runtimePickerBrandLabel(device)}
                      </span>
                      {device.runtime_mode === "cloud" && (
                        <span className="shrink-0 rounded bg-brand/10 px-1.5 py-0.5 text-xs font-medium text-brand">
                          {t(($) => $.create_dialog.runtime_cloud_badge)}
                        </span>
                      )}
                    </div>
                    {showOwner || host ? (
                      <div className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
                        {showOwner && ownerMember ? (
                          <>
                            <ActorAvatar
                              actorType="member"
                              actorId={ownerMember.user_id}
                              size={14}
                            />
                            <span className="truncate">
                              {resolveActorDisplayName(
                                ownerMember,
                                ownerMember.user_id,
                              )}
                            </span>
                          </>
                        ) : null}
                        {!showOwner && host ? (
                          <span className="truncate">{host}</span>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
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
