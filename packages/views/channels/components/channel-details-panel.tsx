"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { Channel, ChannelMemberBrief } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { useT } from "../../i18n";
import { ChannelGroupAvatar } from "./channel-group-avatar";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";

export type ChannelDetailsTab = "about" | "members" | "files" | "settings";

/**
 * LRM-210 — Slack-style channel details docked panel (About | Members | Files | Settings).
 * Replaces fragmented header Share/Stats/Files/Settings chrome.
 */
export function ChannelDetailsPanel({
  channel,
  members,
  tab,
  onTabChange,
  onClose,
  onShare,
  onToggleMute,
  isMuted,
  canManage,
  manageDisabledReason,
  onRename,
  renaming,
  onArchive,
  archivePending,
  projectId,
  wsId,
  onChangeProject,
  projectEditable,
  projectDisabledReason,
  membersContent,
  filesContent,
  variant = "panel",
}: {
  channel: Channel;
  members: ChannelMemberBrief[];
  tab: ChannelDetailsTab;
  onTabChange: (tab: ChannelDetailsTab) => void;
  onClose: () => void;
  onShare: () => void;
  onToggleMute: () => void;
  isMuted: boolean;
  canManage: boolean;
  manageDisabledReason?: string;
  onRename: (name: string) => void;
  renaming?: boolean;
  onArchive: () => void;
  archivePending?: boolean;
  projectId: string | null;
  wsId: string;
  onChangeProject: (projectId: string | null) => void;
  projectEditable: boolean;
  projectDisabledReason?: string;
  membersContent: ReactNode;
  filesContent: ReactNode;
  variant?: "panel" | "page";
}) {
  const { t } = useT("channels");
  const [draftName, setDraftName] = useState(channel.name);
  useEffect(() => {
    setDraftName(channel.name);
  }, [channel.id, channel.name]);
  const nameDirty = draftName.trim() !== channel.name && draftName.trim().length > 0;

  const leading = useMemo(
    () => (
      <div className="flex min-w-0 flex-col">
        <p className="min-w-0 truncate text-sm font-semibold">
          {t(($) => $.details.title)}
        </p>
        <p className="truncate text-xs text-muted-foreground">#{channel.name}</p>
      </div>
    ),
    [channel.name, t],
  );

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={t(($) => $.details.close_aria)}
      leading={leading}
    >
      <Tabs
        value={tab}
        onValueChange={(value) => onTabChange(value as ChannelDetailsTab)}
        className="flex min-h-0 flex-1 flex-col gap-0"
      >
        <div className="shrink-0 border-b px-2">
          <TabsList variant="line" className="h-auto w-full justify-start gap-0">
            <TabsTrigger value="about" className="flex-none px-3 py-2 text-xs">
              {t(($) => $.details.tab_about)}
            </TabsTrigger>
            <TabsTrigger value="members" className="flex-none px-3 py-2 text-xs">
              {t(($) => $.details.tab_members)}
            </TabsTrigger>
            <TabsTrigger value="files" className="flex-none px-3 py-2 text-xs">
              {t(($) => $.details.tab_files)}
            </TabsTrigger>
            <TabsTrigger value="settings" className="flex-none px-3 py-2 text-xs">
              {t(($) => $.details.tab_settings)}
            </TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="about" className="min-h-0 flex-1 overflow-y-auto p-4 text-sm">
          <div className="mb-4 flex items-center gap-3">
            <ChannelGroupAvatar members={members} size={48} />
            <div className="min-w-0">
              <p className="truncate font-semibold">#{channel.name}</p>
              {channel.description ? (
                <p className="mt-0.5 text-xs text-muted-foreground">{channel.description}</p>
              ) : (
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t(($) => $.details.no_description)}
                </p>
              )}
            </div>
          </div>
          <div className="divide-y border-t">
            <div className="flex items-center justify-between gap-3 py-3">
              <span>{t(($) => $.details.notifications)}</span>
              <Button type="button" variant="outline" size="sm" onClick={onToggleMute}>
                {isMuted ? t(($) => $.sidebar.unmute) : t(($) => $.sidebar.mute)}
              </Button>
            </div>
            <div className="flex items-center justify-between gap-3 py-3">
              <span>{t(($) => $.details.share_link)}</span>
              <Button type="button" variant="ghost" size="sm" onClick={onShare}>
                {t(($) => $.details.copy_link)}
              </Button>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="members" className="min-h-0 flex-1 overflow-y-auto">
          {membersContent}
        </TabsContent>

        <TabsContent value="files" className="min-h-0 flex-1 overflow-y-auto p-4">
          {filesContent}
        </TabsContent>

        <TabsContent value="settings" className="min-h-0 flex-1 overflow-y-auto p-4 text-sm">
          {!canManage && manageDisabledReason ? (
            <p className="mb-3 text-xs text-muted-foreground">{manageDisabledReason}</p>
          ) : null}
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-xs font-medium text-muted-foreground" htmlFor="channel-rename">
                {t(($) => $.details.rename_label)}
              </label>
              <div className="flex gap-2">
                <Input
                  id="channel-rename"
                  value={draftName}
                  onChange={(e) => setDraftName(e.target.value)}
                  disabled={!canManage || renaming}
                  className="h-9"
                />
                <Button
                  type="button"
                  size="sm"
                  disabled={!canManage || !nameDirty || renaming}
                  onClick={() => onRename(draftName.trim())}
                >
                  {t(($) => $.details.rename_save)}
                </Button>
              </div>
            </div>

            <ChannelProjectSettingsPanel
              wsId={wsId}
              projectId={projectId}
              onChange={onChangeProject}
              disabled={!projectEditable}
              disabledReason={projectDisabledReason}
            />

            {channel.lark_chat_id ? (
              <div className="rounded-md border px-3 py-2 text-xs text-muted-foreground">
                {t(($) => $.details.lark_bound, { id: channel.lark_chat_id })}
              </div>
            ) : null}

            <div className="border-t pt-3">
              <Button
                type="button"
                variant="ghost"
                className={cn(
                  "h-auto w-full justify-start px-0 py-2 text-destructive hover:bg-transparent hover:text-destructive",
                )}
                disabled={!canManage || archivePending}
                onClick={onArchive}
              >
                {t(($) => $.sidebar.archive)}
              </Button>
              {!canManage ? (
                <p className="text-xs text-muted-foreground">{t(($) => $.sidebar.archive_permission)}</p>
              ) : null}
            </div>
          </div>
        </TabsContent>
      </Tabs>
    </ConversationSidePanelShell>
  );
}
