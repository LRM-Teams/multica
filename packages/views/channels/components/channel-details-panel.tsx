"use client";

import { type ReactNode, type RefObject, useState } from "react";
import { Archive, Bell, BellOff } from "lucide-react";
import type { Channel, ChannelMemberBrief } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { cn } from "@multica/ui/lib/utils";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { useT } from "../../i18n";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ChannelGroupAvatar } from "./channel-group-avatar";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";
import { isConversationMuted } from "./conversation-muted";

export type ChannelDetailsTab = "about" | "members" | "files" | "settings";

/**
 * LRM-210 / LRM-204 — Slack-style channel details: click the channel name to
 * open a right dock (desktop) or bottom drawer page (mobile) with tabs
 * About | Members | Files | Settings. Replaces the old icon-heap of
 * Share / Stats / Files / Settings popovers in the conversation header.
 *
 * Remount via `key={`${channel.id}:${initialTab}`}` when the opener changes
 * tab/channel so draft + tab state reset without derived-state effects.
 */
export function ChannelDetailsPanel({
  channel,
  members,
  wsId,
  projectId,
  projectBound,
  onChangeProject,
  projectEditable,
  projectDisabledReason,
  canManage,
  manageDisabledReason,
  isArchived,
  onMuteToggle,
  mutePending,
  onShare,
  onArchive,
  onRename,
  renamePending,
  onUpdateLarkChatId,
  larkPending,
  membersBody,
  initialTab = "about",
  hideSettingsTab = false,
  onClose,
  variant = "panel",
  portalContainer,
}: {
  channel: Channel;
  members: ChannelMemberBrief[];
  wsId: string;
  projectId: string | null;
  /** Whether a project is currently bound — drives the header subtitle. */
  projectBound: boolean;
  onChangeProject: (projectId: string | null) => void;
  projectEditable: boolean;
  projectDisabledReason?: string;
  canManage: boolean;
  manageDisabledReason?: string;
  isArchived: boolean;
  onMuteToggle: () => void;
  mutePending?: boolean;
  onShare: () => void;
  onArchive: () => void;
  onRename: (name: string) => void;
  renamePending?: boolean;
  onUpdateLarkChatId: (larkChatId: string | null) => void;
  larkPending?: boolean;
  membersBody: ReactNode;
  initialTab?: ChannelDetailsTab;
  hideSettingsTab?: boolean;
  onClose: () => void;
  variant?: "panel" | "page";
  /**
   * DOM node (or ref) to portal the Settings tab's project picker dropdown
   * into, instead of the default `document.body`. #576 — needed when this
   * panel is hosted inside the mobile "..." modal Drawer (`variant="page"`):
   * a modal Dialog locks background interaction by setting
   * `body.style.pointerEvents = "none"` and re-enabling `pointer-events:
   * auto` only on its own content node, so a menu portaled to
   * `document.body` (a DOM sibling of that content, not a descendant)
   * inherits the lock and becomes unclickable. When provided, this same ref
   * is attached to the Settings tab's own wrapper node (a descendant of the
   * Drawer's unlocked content) and forwarded to
   * `ChannelProjectSettingsPanel`, nesting the picker's popup inside the
   * already-unlocked subtree. Left undefined for the desktop docked
   * `variant="panel"` panel, which isn't inside a modal overlay.
   *
   * Typed as a concrete `RefObject` (narrower than
   * `ChannelProjectSettingsPanel`'s own `portalContainer`, which also
   * accepts a raw DOM node) because this same ref doubles as the `ref` of
   * the Settings tab's wrapper `<div>` below.
   */
  portalContainer?: RefObject<HTMLDivElement | null>;
}) {
  const { t } = useT("channels");
  const tabs: { id: ChannelDetailsTab; label: string; hidden?: boolean }[] = [
    { id: "about", label: t(($) => $.details.tab_about) },
    { id: "members", label: t(($) => $.details.tab_members) },
    { id: "files", label: t(($) => $.details.tab_files) },
    {
      id: "settings",
      label: t(($) => $.details.tab_settings),
      hidden: hideSettingsTab,
    },
  ];
  const visibleTabs = tabs.filter((tabItem) => !tabItem.hidden);
  const resolvedInitial =
    hideSettingsTab && initialTab === "settings" ? "about" : initialTab;
  // Local UI state only — parent remounts this panel via key when channel/tab
  // opener changes, so these are not kept in sync through effects.
  // react-doctor-disable-next-line react-doctor/no-derived-state -- intentional editable draft + active tab; remount keyed by channel/initialTab
  const [tab, setTab] = useState<ChannelDetailsTab>(resolvedInitial);
  // react-doctor-disable-next-line react-doctor/no-derived-useState, react-doctor/no-derived-state -- rename/lark drafts; remount keyed by channel.id
  const [nameDraft, setNameDraft] = useState(channel.name);
  // react-doctor-disable-next-line react-doctor/no-derived-useState, react-doctor/no-derived-state -- rename/lark drafts; remount keyed by channel.id
  const [larkDraft, setLarkDraft] = useState(channel.lark_chat_id ?? "");

  const muted = isConversationMuted(channel);
  const nameDirty = nameDraft.trim() !== channel.name;
  const larkDirty = (larkDraft.trim() || null) !== (channel.lark_chat_id || null);
  const settingsEditable = canManage && !isArchived;

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={t(($) => $.details.close_aria)}
      // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell leading slot; remount keyed by channel/tab
      leading={
        <div className="flex min-w-0 flex-col">
          <p className="truncate text-sm font-semibold">{t(($) => $.details.title)}</p>
          <p className="truncate text-xs text-muted-foreground">
            #{channel.name}
            {" · "}
            {projectBound
              ? t(($) => $.details.project_bound)
              : t(($) => $.details.project_unbound)}
          </p>
        </div>
      }
    >
      <div className="flex shrink-0 gap-0.5 overflow-x-auto border-b px-2">
        {visibleTabs.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setTab(item.id)}
            className={cn(
              "shrink-0 border-b-2 px-2.5 py-2.5 text-xs transition-colors",
              tab === item.id
                ? "border-primary font-semibold text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        {tab === "about" && (
          <div className="p-4">
            <div className="mb-4 flex items-center gap-3">
              <ChannelGroupAvatar members={members} size={48} />
              <div className="min-w-0">
                <p className="truncate text-sm font-semibold">#{channel.name}</p>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">
                  {channel.description?.trim()
                    ? channel.description
                    : t(($) => $.details.no_description)}
                </p>
              </div>
            </div>

            <div className="divide-y border-t">
              <div className="flex items-center justify-between gap-3 py-3">
                <span className="text-sm">{t(($) => $.details.notifications)}</span>
                <Switch
                  checked={!muted}
                  disabled={mutePending}
                  onCheckedChange={() => onMuteToggle()}
                  aria-label={
                    muted
                      ? t(($) => $.sidebar.unmute)
                      : t(($) => $.sidebar.mute)
                  }
                />
              </div>
              <button
                type="button"
                onClick={onShare}
                className="flex w-full items-center justify-between gap-3 py-3 text-left text-sm hover:bg-accent/40"
              >
                <span>{t(($) => $.details.share_link)}</span>
                <span className="text-xs text-muted-foreground">
                  {t(($) => $.details.share_copy)}
                </span>
              </button>
              <div className="flex items-center justify-between gap-3 py-3 text-sm text-muted-foreground">
                <span className="inline-flex items-center gap-1.5">
                  {muted ? <BellOff className="size-3.5" /> : <Bell className="size-3.5" />}
                  {muted
                    ? t(($) => $.sidebar.muted_label)
                    : t(($) => $.header.running)}
                </span>
              </div>
            </div>
          </div>
        )}

        {tab === "members" && <div>{membersBody}</div>}

        {tab === "files" && (
          <div className="p-3">
            <ChannelFilesPanel channelId={channel.id} />
          </div>
        )}

        {tab === "settings" && !hideSettingsTab && (
          // #576 — also the portal-container anchor for the project picker's
          // dropdown (see the `portalContainer` prop doc above); only
          // load-bearing for the mobile `variant="page"` Drawer case.
          <div ref={portalContainer}>
            <div className="space-y-4 border-b p-3 md:p-4">
              <div>
                <label className="mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                  {t(($) => $.details.rename_label)}
                </label>
                <div className="flex gap-2">
                  <Input
                    value={nameDraft}
                    onChange={(e) => setNameDraft(e.target.value)}
                    disabled={!settingsEditable || renamePending}
                    aria-label={t(($) => $.details.rename_label)}
                    className="h-9"
                  />
                  <Button
                    type="button"
                    size="sm"
                    disabled={!settingsEditable || !nameDirty || !nameDraft.trim() || renamePending}
                    onClick={() => onRename(nameDraft.trim())}
                  >
                    {t(($) => $.details.save)}
                  </Button>
                </div>
                {!settingsEditable && manageDisabledReason ? (
                  <p className="mt-1.5 text-xs text-muted-foreground">{manageDisabledReason}</p>
                ) : null}
              </div>

              <div>
                <label className="mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                  {t(($) => $.details.lark_label)}
                </label>
                <div className="flex gap-2">
                  <Input
                    value={larkDraft}
                    onChange={(e) => setLarkDraft(e.target.value)}
                    disabled={!settingsEditable || larkPending}
                    placeholder={t(($) => $.sidebar.lark_placeholder)}
                    aria-label={t(($) => $.details.lark_label)}
                    className="h-9 font-mono text-xs"
                  />
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={!settingsEditable || !larkDirty || larkPending}
                    onClick={() => onUpdateLarkChatId(larkDraft.trim() || null)}
                  >
                    {t(($) => $.details.save)}
                  </Button>
                </div>
              </div>
            </div>

            <ChannelProjectSettingsPanel
              wsId={wsId}
              projectId={projectId}
              onChange={onChangeProject}
              disabled={!projectEditable}
              disabledReason={projectDisabledReason}
              portalContainer={portalContainer}
            />

            <div className="border-t p-3 md:p-4">
              <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                {t(($) => $.details.danger_zone)}
              </p>
              {canManage && !isArchived ? (
                <button
                  type="button"
                  onClick={onArchive}
                  className="flex w-full items-center gap-2 py-2.5 text-left text-sm text-destructive hover:opacity-80"
                >
                  <Archive className="size-4 shrink-0" />
                  {t(($) => $.sidebar.archive)}
                </button>
              ) : (
                <div>
                  <button
                    type="button"
                    disabled
                    className="flex w-full cursor-not-allowed items-center gap-2 py-2.5 text-left text-sm text-destructive opacity-50"
                  >
                    <Archive className="size-4 shrink-0" />
                    {t(($) => $.sidebar.archive)}
                  </button>
                  <p className="text-xs text-muted-foreground">
                    {isArchived
                      ? t(($) => $.details.archive_disabled_archived)
                      : manageDisabledReason ?? t(($) => $.sidebar.archive_permission)}
                  </p>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </ConversationSidePanelShell>
  );
}
