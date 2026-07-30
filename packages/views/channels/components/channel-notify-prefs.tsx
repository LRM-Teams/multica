"use client";

import { Bell } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { RadioGroup, RadioGroupItem } from "@multica/ui/components/ui/radio-group";
import { cn } from "@multica/ui/lib/utils";
import type { ChannelNotifyLevel } from "@multica/core/types";
import { channelNotifyLevelLabel } from "./channel-notify-level";
import { useT } from "../../i18n";

type ChannelsT = ReturnType<typeof useT<"channels">>["t"];

const LEVEL_ORDER: readonly ChannelNotifyLevel[] = [
  "default",
  "all",
  "mentions",
  "muted",
];

function levelLabel(t: ChannelsT, level: ChannelNotifyLevel): string {
  return channelNotifyLevelLabel(t, level);
}

function levelHint(t: ChannelsT, level: ChannelNotifyLevel): string {
  switch (level) {
    case "default":
      return t(($) => $.notify_prefs.opt_default_hint);
    case "all":
      return t(($) => $.notify_prefs.opt_all_hint);
    case "mentions":
      return t(($) => $.notify_prefs.opt_mentions_hint);
    case "muted":
      return t(($) => $.notify_prefs.opt_muted_hint);
  }
}

export interface ChannelNotifyPrefsOptionsProps {
  level: ChannelNotifyLevel;
  pending?: boolean;
  onSelect: (level: ChannelNotifyLevel) => void;
  /** Footer「全局通知设置 →」— keeps the pre-LRM-748 navigation target. */
  onOpenGlobalSettings: () => void;
  /** Mobile sub-page uses roomier rows (Slack 1:1), desktop dialog compact. */
  density?: "compact" | "roomy";
}

/**
 * LRM-748 (frozen spec v2) — the four per-channel notify options shared by
 * the desktop dialog and the mobile sub-page. Pure presentation + callback:
 * the caller owns the mutation, so both surfaces stay in sync by construction.
 */
export function ChannelNotifyPrefsOptions({
  level,
  pending = false,
  onSelect,
  onOpenGlobalSettings,
  density = "compact",
}: ChannelNotifyPrefsOptionsProps) {
  const { t } = useT("channels");
  return (
    <div>
      <RadioGroup
        value={level}
        onValueChange={(value) => onSelect(value as ChannelNotifyLevel)}
        disabled={pending}
        aria-label={t(($) => $.notify_prefs.title)}
      >
        {LEVEL_ORDER.map((option) => (
          <label
            key={option}
            className={cn(
              "flex cursor-pointer items-center gap-2.5 px-4",
              density === "roomy"
                ? "border-b border-border/60 py-3.5"
                : "py-2.5",
              pending && "cursor-not-allowed opacity-60",
            )}
            data-testid={`notify-prefs-option-${option}`}
          >
            <RadioGroupItem value={option} />
            <span className="text-[13px] leading-snug">
              {levelLabel(t, option)}
              <span className="mt-0.5 block text-[11px] text-muted-foreground">
                {levelHint(t, option)}
              </span>
            </span>
          </label>
        ))}
      </RadioGroup>
      <button
        type="button"
        className="mt-1.5 flex w-full items-center gap-2 border-t border-border px-4 py-3 text-left text-xs text-primary hover:underline"
        onClick={onOpenGlobalSettings}
        data-testid="notify-prefs-global-settings"
      >
        <Bell className="size-3.5 shrink-0" />
        {t(($) => $.notify_prefs.global_settings)}
      </button>
    </div>
  );
}

export interface ChannelNotifyPrefsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  channelName: string;
  level: ChannelNotifyLevel;
  pending?: boolean;
  onSelect: (level: ChannelNotifyLevel) => void;
  onOpenGlobalSettings: () => void;
}

/** LRM-748 — desktop: notify prefs live in a dialog, never a page push, so
 *  closing lands right back in the conversation. */
export function ChannelNotifyPrefsDialog({
  open,
  onOpenChange,
  channelName,
  level,
  pending = false,
  onSelect,
  onOpenGlobalSettings,
}: ChannelNotifyPrefsDialogProps) {
  const { t } = useT("channels");
  const channelLabel = channelName.startsWith("#")
    ? channelName
    : `#${channelName}`;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="gap-0 overflow-hidden border-border p-0 sm:max-w-sm"
        data-testid="channel-notify-prefs-dialog"
        onClick={(e) => e.stopPropagation()}
      >
        <DialogHeader className="px-4 pb-2 pt-4 text-left">
          <DialogTitle className="text-sm font-bold">
            {t(($) => $.notify_prefs.title)}
          </DialogTitle>
          <DialogDescription className="text-[11px]">
            {channelLabel}
          </DialogDescription>
        </DialogHeader>
        <ChannelNotifyPrefsOptions
          level={level}
          pending={pending}
          onSelect={onSelect}
          onOpenGlobalSettings={onOpenGlobalSettings}
        />
      </DialogContent>
    </Dialog>
  );
}
