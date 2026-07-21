"use client";

import { useMemo } from "react";
import type { Channel, ChannelMemberBrief } from "@multica/core/types";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { ChannelGroupAvatar } from "./channel-group-avatar";
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
 */
export function ChannelSettingsSidePanel({
  channel,
  members,
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
      <>
        <ChannelGroupAvatar members={members} size={32} />
        <div className="flex min-w-0 flex-col">
          <p className="min-w-0 truncate text-sm font-semibold">{channel.name}</p>
          <p className="truncate text-xs text-muted-foreground">{settingsLabel}</p>
        </div>
      </>
    ),
    [members, channel.name, settingsLabel],
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
