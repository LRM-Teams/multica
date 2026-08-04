"use client";

import { useMemo, useState } from "react";
import { Cloud, Monitor } from "lucide-react";
import type { AgentRuntime, MemberWithUser } from "@multica/core/types";
import { ActorAvatar } from "../../../common/actor-avatar";
import {
  PickerItem,
  PropertyPicker,
} from "../../../issues/components/pickers";
import { ProviderLogo } from "../../../runtimes/components/provider-logo";
import { filterRuntimesOnBoundComputer } from "../../../runtimes/components/runtime-machines";
import { CHIP_CLASS } from "./chip";
import { useT } from "../../../i18n";
import {
  runtimePickerBrandLabel,
  runtimePickerHostSubtitle,
} from "../runtime-picker-labels";

/**
 * Inline runtime/code-agent picker for the agent inspector.
 * Computer is bound at create time and shown as a separate read-only row —
 * this picker only lists code agents on the same computer, so changing it
 * cannot move the agent to another machine.
 */
export function RuntimePicker({
  value,
  runtimes,
  members,
  currentUserId,
  canEdit = true,
  /**
   * Agent's saved runtime id — locks the option list to that computer even
   * while the draft `value` changes inside RuntimeConfigDialog (LRM-1365).
   * Defaults to `value` when omitted (read-only summary / single-shot edit).
   */
  boundRuntimeId,
  onChange,
}: {
  value: string;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  /** When false, render a static read-only display and skip the popover. */
  canEdit?: boolean;
  boundRuntimeId?: string;
  onChange: (runtimeId: string) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);

  const selected = runtimes.find((r) => r.id === value) ?? null;
  const Icon = selected?.runtime_mode === "cloud" ? Cloud : Monitor;
  // Lock to the agent's bound computer (saved runtime), not the draft
  // selection — otherwise picking a mis-grouped option could expand the
  // list onto another machine. Prefer explicit boundRuntimeId from the
  // dialog; fall back to value. When that runtime is missing from
  // `runtimes` (deleted / not loaded), fall back to the full list so the
  // user can re-bind (orphan recovery) — not a cross-machine move of a
  // still-present binding.
  const lockId = boundRuntimeId || value;
  const sameComputerRuntimes = useMemo(() => {
    const boundRuntime = runtimes.find((r) => r.id === lockId) ?? null;
    if (!boundRuntime) return runtimes;
    return filterRuntimesOnBoundComputer(boundRuntime, runtimes);
  }, [runtimes, lockId]);

  // Others' private runtimes are excluded outright, not shown-disabled — a
  // private runtime that isn't mine and isn't public has nothing for me to
  // do with it.
  const filtered = useMemo(() => {
    const isUsable = (r: AgentRuntime): boolean => {
      if (!currentUserId) return true;
      if (r.owner_id === currentUserId) return true;
      return r.visibility === "public";
    };
    return sameComputerRuntimes.filter(isUsable).toSorted((a, b) => {
      const aMine = a.owner_id === currentUserId;
      const bMine = b.owner_id === currentUserId;
      if (aMine && !bMine) return -1;
      if (!aMine && bMine) return 1;
      return 0;
    });
  }, [sameComputerRuntimes, currentUserId]);

  const brandLabel = selected
    ? runtimePickerBrandLabel(selected)
    : t(($) => $.pickers.runtime_none);

  if (!canEdit) {
    return (
      <span className="inline-flex min-w-0 items-center gap-1.5 px-1.5 py-0.5 text-xs text-muted-foreground">
        <Icon className="h-3 w-3 shrink-0" />
        <span className="min-w-0 truncate">{brandLabel}</span>
      </span>
    );
  }

  const triggerTitle = selected
    ? brandLabel
    : t(($) => $.pickers.runtime_tooltip_none);

  const getOwner = (id: string | null) =>
    id ? (members.find((m) => m.user_id === id) ?? null) : null;

  const select = async (id: string) => {
    setOpen(false);
    if (id !== value) await onChange(id);
  };

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-auto min-w-[18rem] max-w-md"
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
          {selected ? (
            <ProviderLogo
              provider={selected.provider}
              className="h-3 w-3 shrink-0"
            />
          ) : (
            <Icon className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 truncate">{brandLabel}</span>
        </>
      }
    >
      {filtered.length === 0 ? (
        <p className="px-2 py-3 text-center text-xs text-muted-foreground">
          {t(($) => $.pickers.runtime_empty)}
        </p>
      ) : (
        filtered.map((rt) => {
          const owner = getOwner(rt.owner_id);
          const isMine = !!currentUserId && rt.owner_id === currentUserId;
          const showOwner = !isMine && !!owner;
          const host = runtimePickerHostSubtitle(rt, filtered);
          return (
            <PickerItem
              key={rt.id}
              selected={rt.id === value}
              onClick={() => void select(rt.id)}
              tooltip={runtimePickerBrandLabel(rt)}
            >
              <ProviderLogo
                provider={rt.provider}
                className="h-4 w-4 shrink-0"
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <span className="truncate text-sm font-medium">
                    {runtimePickerBrandLabel(rt)}
                  </span>
                  {rt.runtime_mode === "cloud" && (
                    <span className="shrink-0 rounded bg-brand/10 px-1 text-[10px] font-medium text-brand">
                      {t(($) => $.create_dialog.runtime_cloud_badge)}
                    </span>
                  )}
                </div>
                {showOwner || host ? (
                  <div className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
                    {showOwner && owner ? (
                      <span className="flex min-w-0 items-center gap-1">
                        <ActorAvatar
                          actorType="member"
                          actorId={owner.user_id}
                          size={12}
                        />
                        <span className="truncate">{owner.name}</span>
                      </span>
                    ) : null}
                    {!showOwner && host ? (
                      <span className="truncate">{host}</span>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </PickerItem>
          );
        })
      )}
    </PropertyPicker>
  );
}
