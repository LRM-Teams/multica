"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Globe, Hash, Lock, Users } from "lucide-react";
import {
  VISIBILITY_DESCRIPTION,
  VISIBILITY_LABEL,
  VISIBILITY_OPTIONS,
  VISIBILITY_TOOLTIP,
} from "@multica/core/agents";
import { channelsOptions } from "@multica/core/channels";
import { useWorkspaceId } from "@multica/core/hooks";
import type { AgentVisibility } from "@multica/core/types";
import {
  PickerItem,
  PropertyPicker,
} from "../../../issues/components/pickers";
import { useT } from "../../../i18n";
import { VisibilityBadge } from "../visibility-badge";
import { CHIP_CLASS } from "./chip";
import { HomeChannelBindChip } from "../home-channel-bind-chip";

export type VisibilityChange = {
  visibility: AgentVisibility;
  /** Set when selecting/updating channel; `null` when leaving channel. */
  home_channel_id: string | null;
};

function VisibilityIcon({
  value,
  className,
}: {
  value: AgentVisibility;
  className?: string;
}) {
  if (value === "private") return <Lock className={className} />;
  if (value === "channel") return <Hash className={className} />;
  return <Globe className={className} />;
}

export function VisibilityPicker({
  value,
  homeChannelId = null,
  canEdit = true,
  onChange,
}: {
  value: AgentVisibility;
  homeChannelId?: string | null;
  /** When false, render a read-only `<VisibilityBadge>` and skip the popover. */
  canEdit?: boolean;
  onChange: (next: VisibilityChange) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);
  const [draftHome, setDraftHome] = useState<string | null>(homeChannelId);
  const wsId = useWorkspaceId();
  const { data: channels = [], isLoading: channelsLoading } = useQuery(
    channelsOptions(wsId),
  );
  const groups = useMemo(
    () => channels.filter((c) => c.kind === "group" && !c.archived_at),
    [channels],
  );
  const channelDisabled = !channelsLoading && groups.length === 0;

  if (!canEdit) {
    return (
      <VisibilityBadge value={value} homeChannelId={homeChannelId} />
    );
  }

  const label = VISIBILITY_LABEL[value];
  const tooltip = `Visibility · ${VISIBILITY_TOOLTIP[value]}`;

  const select = async (next: AgentVisibility, home: string | null) => {
    if (next === "channel") {
      if (!home) {
        // Stay open so the user can bind; do not call onChange yet and do
        // not silently fall back to private (LRM-238).
        setDraftHome(null);
        return;
      }
      setOpen(false);
      if (next !== value || home !== homeChannelId) {
        await onChange({ visibility: "channel", home_channel_id: home });
      }
      return;
    }
    setOpen(false);
    if (next !== value || homeChannelId != null) {
      await onChange({ visibility: next, home_channel_id: null });
    }
  };

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) {
      setDraftHome(
        homeChannelId ?? groups[0]?.id ?? null,
      );
    }
  };

  return (
    <PropertyPicker
      open={open}
      onOpenChange={onOpenChange}
      width="w-auto min-w-[16rem]"
      align="start"
      tooltip={tooltip}
      triggerRender={
        <button type="button" className={CHIP_CLASS} aria-label={tooltip} />
      }
      trigger={
        <>
          <VisibilityIcon
            value={value}
            className="h-3 w-3 shrink-0 text-muted-foreground"
          />
          <span className="truncate">{label}</span>
          {value === "channel" && homeChannelId ? (
            <Users className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : null}
        </>
      }
    >
      {VISIBILITY_OPTIONS.map((option) => {
        const selected = value === option;
        const disabled = option === "channel" && channelDisabled;
        return (
          <PickerItem
            key={option}
            selected={selected}
            disabled={disabled}
            onClick={() => {
              if (disabled) return;
              if (option === "channel") {
                const home = draftHome ?? groups[0]?.id ?? null;
                void select("channel", home);
                return;
              }
              void select(option, null);
            }}
          >
            <VisibilityIcon
              value={option}
              className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
            />
            <div className="min-w-0 flex-1 text-left">
              <div className="font-medium">{VISIBILITY_LABEL[option]}</div>
              <div className="text-xs text-muted-foreground">
                {VISIBILITY_DESCRIPTION[option]}
              </div>
              {option === "channel" && (selected || open) ? (
                <div className="mt-1.5" onClick={(e) => e.stopPropagation()}>
                  <HomeChannelBindChip
                    value={selected ? homeChannelId : draftHome}
                    onChange={(id) => {
                      setDraftHome(id);
                      if (selected) {
                        void onChange({
                          visibility: "channel",
                          home_channel_id: id,
                        });
                      } else {
                        void select("channel", id);
                      }
                    }}
                    disabled={disabled}
                  />
                </div>
              ) : null}
              {option === "channel" && disabled ? (
                <div className="mt-1 text-xs text-muted-foreground">
                  {t(($) => $.visibility_bind.no_groups)}
                </div>
              ) : null}
            </div>
          </PickerItem>
        );
      })}
      <p className="px-2 pb-1.5 pt-0.5 text-[10px] leading-snug text-muted-foreground">
        {t(($) => $.visibility_bind.not_channel_permission_hint)}
      </p>
    </PropertyPicker>
  );
}
