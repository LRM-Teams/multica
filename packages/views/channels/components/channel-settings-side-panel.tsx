"use client";

import { useMemo } from "react";
import type { Channel, ChannelMemberBrief } from "@multica/core/types";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { ChannelHashLandmark } from "./channel-hash-landmark";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";
import { useT } from "../../i18n";

/**
 * #645 — Group Settings docked panel: the same shell family as
 * `AgentSidePanel` (per Frank/Iris — "布局要收敛", stop inventing one-off
 * cards), sharing its exclusive right-side slot with the Thread and Agent
 * panels on desktop, and the same body reused as a full-width mobile
 * Drawer sub-page (`variant="page"`).
 *
 * Only the Project section exists today; future settings (members,
 * integrations, ...) are additional sections in the body, not new header
 * chrome.
 *
 * LRM-254 A1 — header leading is text-level `#` + name (no member collage).
 */
export function ChannelSettingsSidePanel({
  channel,
  members: _members,
  wsId,
  projectId,
  onChangeProject,
  projectEditable,
  projectDisabledReason,
  onClose,
  variant = "panel",
}: {
  channel: Channel;
  members: ChannelMemberBrief[];
  wsId: string;
  projectId: string | null;
  onChangeProject: (projectId: string | null) => void;
  projectEditable: boolean;
  projectDisabledReason?: string;
  onClose: () => void;
  variant?: "panel" | "page";
}) {
  const { t } = useT("channels");
  const settingsLabel = t(($) => $.settings.title);
  const leading = useMemo(
    () => (
      <div className="flex min-w-0 items-center gap-1.5">
        <ChannelHashLandmark size="sm" avatarUrl={channel.avatar_url} />
        <div className="flex min-w-0 flex-col">
          <p className="min-w-0 truncate text-sm font-semibold">{channel.name}</p>
          <p className="truncate text-xs text-muted-foreground">{settingsLabel}</p>
        </div>
      </div>
    ),
    [channel.name, channel.avatar_url, settingsLabel],
  );

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={t(($) => $.settings.close_aria)}
      leading={leading}
    >
      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <ChannelProjectSettingsPanel
          wsId={wsId}
          projectId={projectId}
          onChange={onChangeProject}
          disabled={!projectEditable}
          disabledReason={projectDisabledReason}
        />
      </div>
    </ConversationSidePanelShell>
  );
}
