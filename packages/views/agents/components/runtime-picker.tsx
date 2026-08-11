"use client";

import { useMemo, useState } from "react";
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
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  executionFieldClass,
  executionOptionClass,
  executionOptionSelectedClass,
  executionTriggerClass,
} from "./execution-picker-styles";
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
  disabled = false,
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
  disabled?: boolean;
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
  const sortedRuntimes = useMemo(
    () => sortRuntimesForPicker(runtimes, currentUserId),
    [runtimes, currentUserId],
  );

  const selectedRuntime =
    runtimes.find((d) => d.id === selectedRuntimeId) ?? null;

  return (
    <div className={executionFieldClass}>
      <Label className="text-xs font-medium text-muted-foreground">
        {pickerLabel}
      </Label>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          disabled={disabled || (runtimes.length === 0 && !runtimesLoading)}
          data-testid="runtime-picker-trigger"
          className={executionTriggerClass}
        >
          {runtimesLoading ? (
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />
          ) : selectedRuntime ? (
            <ProviderLogo
              provider={selectedRuntime.provider}
              className="h-3.5 w-3.5 shrink-0"
            />
          ) : (
            <Cloud className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 flex-1 truncate">
            {runtimesLoading
              ? t(($) => $.create_dialog.runtime_loading)
              : selectedRuntime
                ? runtimePickerBrandLabel(selectedRuntime)
                : t(($) => $.create_dialog.runtime_none)}
          </span>
          {selectedRuntime?.runtime_mode === "cloud" ? (
            <span className="shrink-0 rounded bg-brand/10 px-1.5 py-0.5 text-[10px] font-medium text-brand">
              {t(($) => $.create_dialog.runtime_cloud_badge)}
            </span>
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
          {sortedRuntimes.length === 0 ? (
            <p
              className="px-2.5 py-1.5 text-center text-xs text-muted-foreground"
              data-testid="runtime-picker-empty"
            >
              {t(($) => $.create_dialog.runtime_empty)}
            </p>
          ) : (
            sortedRuntimes.map((device) => {
              const ownerMember = getOwnerMember(device.owner_id);
              const isMine =
                !!currentUserId && device.owner_id === currentUserId;
              const host = runtimePickerHostSubtitle(device, sortedRuntimes);
              const showOwner = !isMine && !!ownerMember;
              const secondary = showOwner && ownerMember
                ? resolveActorDisplayName(ownerMember, ownerMember.user_id)
                : host;
              return (
                <button
                  key={device.id}
                  type="button"
                  data-testid={`runtime-picker-option-${device.id}`}
                  onClick={() => {
                    onSelect(device.id);
                    setOpen(false);
                  }}
                  className={cn(
                    executionOptionClass,
                    device.id === selectedRuntimeId &&
                      executionOptionSelectedClass,
                  )}
                >
                  <ProviderLogo
                    provider={device.provider}
                    className="h-3.5 w-3.5 shrink-0"
                  />
                  <span className="min-w-0 flex-1 truncate">
                    {runtimePickerBrandLabel(device)}
                    {secondary ? (
                      <span className="text-muted-foreground">
                        {" · "}
                        {secondary}
                      </span>
                    ) : null}
                  </span>
                  {showOwner && ownerMember ? (
                    <ActorAvatar
                      actorType="member"
                      actorId={ownerMember.user_id}
                      size={14}
                    />
                  ) : null}
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
