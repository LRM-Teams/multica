"use client";

import { HomeChannelBindChip } from "./home-channel-bind-chip";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

export type HomeChannelMode = "existing" | "new";

/**
 * 「仅本群」绑定区：选已有群，或现场新建群（默认名可改）。
 * 无群时只能走新建；有群时两种都可选。
 */
export function HomeChannelBindPanel({
  mode,
  onModeChange,
  existingChannelId,
  onExistingChannelChange,
  newChannelName,
  onNewChannelNameChange,
  invalid = false,
  hasGroups,
  className,
}: {
  mode: HomeChannelMode;
  onModeChange: (mode: HomeChannelMode) => void;
  existingChannelId: string | null;
  onExistingChannelChange: (channelId: string) => void;
  newChannelName: string;
  onNewChannelNameChange: (name: string) => void;
  invalid?: boolean;
  hasGroups: boolean;
  className?: string;
}) {
  const { t } = useT("agents");
  const effectiveMode: HomeChannelMode = hasGroups ? mode : "new";

  return (
    <div className={cn("space-y-2", className)}>
      <fieldset className="min-w-0 border-0 p-0">
        <legend className="sr-only">
          {t(($) => $.create_dialog.visibility_label)}
        </legend>
        <div className="inline-flex max-w-full flex-wrap gap-1 rounded-md border bg-muted/40 p-0.5">
          <button
            type="button"
            disabled={!hasGroups}
            aria-pressed={effectiveMode === "existing"}
            onClick={() => onModeChange("existing")}
            className={cn(
              "rounded px-2 py-1 text-xs font-medium transition-colors",
              effectiveMode === "existing"
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
              !hasGroups && "cursor-not-allowed opacity-50",
            )}
          >
            {t(($) => $.visibility_bind.home_mode_existing)}
          </button>
          <button
            type="button"
            aria-pressed={effectiveMode === "new"}
            onClick={() => onModeChange("new")}
            className={cn(
              "rounded px-2 py-1 text-xs font-medium transition-colors",
              effectiveMode === "new"
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {t(($) => $.visibility_bind.home_mode_new)}
          </button>
        </div>
      </fieldset>

      {effectiveMode === "existing" ? (
        <HomeChannelBindChip
          value={existingChannelId}
          invalid={invalid}
          onChange={onExistingChannelChange}
        />
      ) : (
        <div className="space-y-1">
          <Label htmlFor="home-channel-new-name" className="text-xs text-muted-foreground">
            {t(($) => $.visibility_bind.new_channel_name_label)}
          </Label>
          <Input
            id="home-channel-new-name"
            value={newChannelName}
            onChange={(e) => onNewChannelNameChange(e.target.value)}
            placeholder={t(($) => $.visibility_bind.new_channel_name_placeholder)}
            aria-invalid={invalid}
            className={cn(
              "h-8 text-xs",
              invalid && "border-destructive focus-visible:ring-destructive/40",
            )}
          />
          {!hasGroups ? (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.visibility_bind.no_groups)}
            </p>
          ) : null}
        </div>
      )}
    </div>
  );
}
