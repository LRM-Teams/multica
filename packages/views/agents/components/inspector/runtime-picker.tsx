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
import { CHIP_CLASS } from "./chip";
import { useT } from "../../../i18n";
import {
  runtimePickerBrandLabel,
  runtimePickerHostSubtitle,
} from "../runtime-picker-labels";
import { runtimePickerOptions } from "./runtime-picker-options";

/**
 * Inline runtime/code-agent picker for the agent inspector.
 * The selected runtime also determines the agent's computer. Durable memory is
 * synchronized before each turn, so eligible runtimes on other computers are
 * valid move targets; server capability gates reject outdated target daemons.
 */
export function RuntimePicker({
  value,
  runtimes,
  members,
  currentUserId,
  canEdit = true,
  onChange,
}: {
  value: string;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  /** When false, render a static read-only display and skip the popover. */
  canEdit?: boolean;
  /** Retained for RuntimeConfigDialog compatibility; cross-machine moves no longer lock to it. */
  boundRuntimeId?: string;
  onChange: (runtimeId: string) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);

  const selected = runtimes.find((r) => r.id === value) ?? null;
  const Icon = selected?.runtime_mode === "cloud" ? Cloud : Monitor;
  // Others' private runtimes are excluded outright, not shown-disabled — a
  // private runtime that isn't mine and isn't public has nothing for me to
  // do with it.
  const filtered = useMemo(
    () => runtimePickerOptions(runtimes, currentUserId),
    [runtimes, currentUserId],
  );

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
