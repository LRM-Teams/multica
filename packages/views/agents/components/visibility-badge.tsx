"use client";

import { Globe, Hash, Lock } from "lucide-react";
import type { AgentVisibility } from "@multica/core/types";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";
import { useHomeChannelName } from "./home-channel-bind-chip";

/**
 * Read-only visibility badge — used wherever a user should *see* an agent's
 * visibility (Personal / 仅本群 / Workspace) without being able to change it.
 * Replaces the interactive `<VisibilityPicker>` for non-managers on the detail
 * page, and is also the canonical badge for hover cards and list rows.
 *
 * `compact` drops the text label and shows just the icon — for tight spaces
 * like the agent table where the column header already labels the field.
 * Channel visibility always keeps the「仅本群」label + #chip (AC for LRM-371).
 */
export function VisibilityBadge({
  value,
  homeChannelId = null,
  compact = false,
  className = "",
}: {
  value: AgentVisibility;
  homeChannelId?: string | null;
  compact?: boolean;
  className?: string;
}) {
  const { t } = useT("agents");
  const { name: homeName, missing } = useHomeChannelName(homeChannelId);
  const Icon =
    value === "private" ? Lock : value === "channel" ? Hash : Globe;
  const label = t(($) => $.visibility[value].label);
  const tooltip = t(($) => $.visibility[value].tooltip);

  if (value === "channel") {
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              className={`inline-flex min-w-0 items-center gap-1 text-xs text-muted-foreground ${className}`}
              aria-label={tooltip}
              data-testid="visibility-badge-channel"
            >
              <Icon className="h-3 w-3 shrink-0" />
              <span className="truncate">{label}</span>
              {homeName ? (
                <span className="inline-flex max-w-28 items-center gap-0.5 truncate rounded-md border border-border bg-muted px-1.5 py-0.5 text-[10px] font-medium text-foreground">
                  <span className="text-muted-foreground">#</span>
                  <span className="truncate">{homeName}</span>
                </span>
              ) : missing ? (
                <span className="truncate text-destructive">
                  {t(($) => $.visibility_bind.channel_unavailable)}
                </span>
              ) : homeChannelId ? null : (
                <span className="truncate text-destructive">
                  {t(($) => $.visibility_bind.home_required)}
                </span>
              )}
            </span>
          }
        />
        <TooltipContent>{tooltip}</TooltipContent>
      </Tooltip>
    );
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={`inline-flex items-center gap-1 text-xs text-muted-foreground ${className}`}
            aria-label={tooltip}
          >
            <Icon className="h-3 w-3 shrink-0" />
            {!compact && <span className="truncate">{label}</span>}
          </span>
        }
      />
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  );
}
