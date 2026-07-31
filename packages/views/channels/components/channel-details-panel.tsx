"use client";

import {
  type ChangeEvent,
  type ReactNode,
  type RefObject,
  useReducer,
  useRef,
} from "react";
import {
  Bell,
  ChevronLeft,
  ChevronRight,
  Search,
  Settings,
  Square,
  Trash2,
  VolumeX,
} from "lucide-react";
import type { Channel, ChannelMemberBrief, ChannelNotifyLevel } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { useT } from "../../i18n";
import { ChannelDetailsDetailRow } from "./channel-details-detail-row";
import { ChannelDetailsHeroAvatar } from "./channel-details-hero-avatar";
import { ChannelDetailsMemberStack } from "./channel-details-member-stack";
import { GroupManagerHint } from "./group-manager-hint";
import { MotionContent } from "../../common/motion-content";
import { ChannelDetailsSectionCard } from "./channel-details-section-card";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";
import { isConversationMuted } from "./conversation-muted";
import { ChannelNotifyPrefsOptions } from "./channel-notify-prefs";

/**
 * LRM-494 — Slack-style channel details surface.
 * Home is a single overview (hero + section cards + danger). Drill-down
 * reuses Members/Settings bodies as sub-views.
 * LRM-860 — About sub-views removed; hero name/description/avatar edit in place.
 * LRM-675 — the Files drill-down is removed: the main-area 「文件」 tab is
 * the single Files entry (no dual-track entry, LRM-238).
 */
export type ChannelDetailsTab = "about" | "members" | "settings";
type DetailsView = "home" | ChannelDetailsTab | "notify-prefs";
type HeroEditField = "name" | "description" | null;

/** Panel UI state — one reducer so related drafts don't fan out renders (react-doctor). */
type PanelUiState = {
  view: DetailsView;
  nameDraft: string;
  descriptionDraft: string;
  larkDraft: string;
  heroEdit: HeroEditField;
};

type PanelUiAction =
  | { type: "set_view"; view: DetailsView }
  | { type: "set_name_draft"; value: string }
  | { type: "set_description_draft"; value: string }
  | { type: "set_lark_draft"; value: string }
  | { type: "begin_name_edit"; name: string }
  | { type: "begin_description_edit"; description: string }
  | { type: "cancel_name"; name: string }
  | { type: "cancel_description"; description: string }
  | { type: "end_hero_edit" };

function panelUiReducer(state: PanelUiState, action: PanelUiAction): PanelUiState {
  switch (action.type) {
    case "set_view":
      return { ...state, view: action.view };
    case "set_name_draft":
      return { ...state, nameDraft: action.value };
    case "set_description_draft":
      return { ...state, descriptionDraft: action.value };
    case "set_lark_draft":
      return { ...state, larkDraft: action.value };
    case "begin_name_edit":
      return { ...state, nameDraft: action.name, heroEdit: "name" };
    case "begin_description_edit":
      return { ...state, descriptionDraft: action.description, heroEdit: "description" };
    case "cancel_name":
      return { ...state, nameDraft: action.name, heroEdit: null };
    case "cancel_description":
      return { ...state, descriptionDraft: action.description, heroEdit: null };
    case "end_hero_edit":
      return { ...state, heroEdit: null };
    default:
      return state;
  }
}

/** Caps capability / pending flags so the panel avoids many boolean props (react-doctor). */
export type ChannelDetailsAccess = {
  canManage: boolean;
  isArchived: boolean;
  hideSettingsTab: boolean;
  projectBound: boolean;
  projectEditable: boolean;
  stopAllDisabled?: boolean;
  mutePending?: boolean;
  renamePending?: boolean;
  descriptionPending?: boolean;
  larkPending?: boolean;
  avatarPending?: boolean;
};

function resolveInitialView(
  initialTab: ChannelDetailsTab,
  hideSettingsTab: boolean,
): DetailsView {
  if (hideSettingsTab && initialTab === "settings") return "home";
  if (initialTab === "about") return "home";
  return initialTab;
}

export function ChannelDetailsPanel({
  channel,
  members,
  wsId,
  projectId,
  onChangeProject,
  projectDisabledReason,
  access,
  manageDisabledReason,
  onMuteToggle,
  onShare: _onShare,
  onArchive,
  onDelete,
  onRename,
  onUpdateDescription,
  onUpdateLarkChatId,
  onUpdateAvatar,
  membersBody,
  initialTab = "about",
  onClose,
  variant = "panel",
  portalContainer,
  onOpenSearch,
  onStopAllAgents,
  stopAllDisabledReason,
  notifyPrefLabel,
  onOpenNotificationPrefs,
  notifyLevel,
  onSelectNotifyLevel,
  notifyLevelPending,
  onOpenGlobalNotifySettings,
  groupLeave,
}: {
  channel: Channel;
  members: ChannelMemberBrief[];
  wsId: string;
  projectId: string | null;
  onChangeProject: (projectId: string | null) => void;
  projectDisabledReason?: string;
  access: ChannelDetailsAccess;
  manageDisabledReason?: string;
  onMuteToggle: () => void;
  onShare: () => void;
  onArchive: () => void;
  /**
   * LRM-239 — permanent delete (owner/admin only). Omit to hide the delete
   * entry entirely (members / creator-members / system surfaces).
   */
  onDelete?: () => void;
  onRename: (name: string) => void;
  onUpdateDescription?: (description: string | null) => void;
  onUpdateLarkChatId: (larkChatId: string | null) => void;
  /** LRM-724 — persists the uploaded channel icon (already-uploaded link). */
  onUpdateAvatar?: (avatarUrl: string) => void;
  membersBody: ReactNode;
  initialTab?: ChannelDetailsTab;
  onClose: () => void;
  variant?: "panel" | "page";
  /**
   * DOM node (or ref) to portal the Settings tab's project picker dropdown
   * into, instead of the default `document.body`. #576 — needed when this
   * panel is hosted inside the mobile page Drawer (`variant="page"`).
   */
  portalContainer?: RefObject<HTMLDivElement | null>;
  /** LRM-494 — channel-scoped search (not global). */
  onOpenSearch?: () => void;
  onStopAllAgents?: () => void;
  stopAllDisabledReason?: string;
  /** LRM-494 — live preference label from workspace notify settings (LRM-414). */
  notifyPrefLabel: string;
  /** Desktop (variant="panel"): close the panel and open the notify-prefs
   *  dialog hosted by the surface. Mobile (variant="page") drills into the
   *  internal "notify-prefs" sub-view instead (LRM-748 frozen v2). */
  onOpenNotificationPrefs?: () => void;
  /** LRM-748 — four-level per-channel notify preference (frozen v2). */
  notifyLevel?: ChannelNotifyLevel;
  onSelectNotifyLevel?: (level: ChannelNotifyLevel) => void;
  notifyLevelPending?: boolean;
  onOpenGlobalNotifySettings?: () => void;
  /**
   * Group leave affordance (group management), rendered in the danger zone.
   * `onLeave` present → destructive + clickable. Only `disabledReason` → shown
   * disabled with that reason (owner must transfer ownership first; or member
   * self-leave not yet wired — real mutation lands once BE confirms self-DELETE,
   * per Iris we never fake-click). Omit → not rendered (DM / system / non-group).
   */
  groupLeave?: { onLeave?: () => void; disabledReason?: string };
}) {
  const { t } = useT("channels");
  const {
    canManage,
    isArchived,
    hideSettingsTab,
    stopAllDisabled,
    mutePending,
    renamePending,
    descriptionPending,
    larkPending,
    avatarPending,
    projectBound,
    projectEditable,
  } = access;

  // Parent remounts via key when channel/tab opener changes — drafts/view
  // reset with that remount (not synced through effects).
  const [ui, dispatch] = useReducer(panelUiReducer, undefined, () => ({
    view: resolveInitialView(initialTab, hideSettingsTab),
    nameDraft: channel.name,
    descriptionDraft: channel.description ?? "",
    larkDraft: channel.lark_chat_id ?? "",
    heroEdit: null as HeroEditField,
  }));
  const { view, nameDraft, descriptionDraft, larkDraft, heroEdit } = ui;
  const skipHeroBlurRef = useRef(false);

  // LRM-724 / LRM-860 — channel icon upload from hero (no avatar sub-view).
  const avatarInputRef = useRef<HTMLInputElement>(null);
  const { upload: uploadAvatarFile, uploading: avatarUploading } = useFileUpload(api);
  const avatarBusy = avatarUploading || !!avatarPending;

  const handleAvatarFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = ""; // allow re-selecting the same file
    if (!file.type.startsWith("image/")) {
      showErrorToast(t(($) => $.details.avatar_select_image));
      return;
    }
    try {
      const result = await uploadAvatarFile(file, { channelId: channel.id });
      if (!result) return;
      onUpdateAvatar?.(result.link);
    } catch {
      showErrorToast(t(($) => $.details.avatar_upload_failed));
    }
  };

  const muted = isConversationMuted(channel);
  const larkDirty = (larkDraft.trim() || null) !== (channel.lark_chat_id || null);
  const settingsEditable = canManage && !isArchived;

  const commitName = () => {
    if (skipHeroBlurRef.current) {
      skipHeroBlurRef.current = false;
      return;
    }
    const next = nameDraft.trim();
    if (!next) {
      showErrorToast(t(($) => $.details.rename_empty));
      return;
    }
    if (next !== channel.name) onRename(next);
    dispatch({ type: "end_hero_edit" });
  };

  const cancelName = () => {
    skipHeroBlurRef.current = true;
    dispatch({ type: "cancel_name", name: channel.name });
  };

  const commitDescription = () => {
    if (skipHeroBlurRef.current) {
      skipHeroBlurRef.current = false;
      return;
    }
    const next = descriptionDraft.trim() || null;
    const prev = channel.description?.trim() || null;
    if (next !== prev) onUpdateDescription?.(next);
    dispatch({ type: "end_hero_edit" });
  };

  const cancelDescription = () => {
    skipHeroBlurRef.current = true;
    dispatch({
      type: "cancel_description",
      description: channel.description ?? "",
    });
  };

  const userCount = members.filter((m) => m.member_type === "user").length;
  const agentCount = members.filter((m) => m.member_type === "agent").length;

  const goHome = () => dispatch({ type: "set_view", view: "home" });

  const subTitle =
    view === "members"
      ? t(($) => $.details.tab_members)
      : view === "settings"
        ? t(($) => $.details.section_settings)
        : view === "notify-prefs"
          ? t(($) => $.notify_prefs.title)
          : t(($) => $.details.title);

  const leading =
    view === "home" ? (
      <p className="truncate text-sm font-semibold">{t(($) => $.details.title)}</p>
    ) : (
      <div className="flex min-w-0 items-center gap-1">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          onClick={goHome}
          aria-label={t(($) => $.details.back_aria)}
          data-testid="channel-details-back"
        >
          <ChevronLeft className="size-5" />
        </Button>
        <p className="truncate text-sm font-semibold">{subTitle}</p>
      </div>
    );

  return (
    <ConversationSidePanelShell
      variant={variant}
      onClose={onClose}
      closeAriaLabel={t(($) => $.details.close_aria)}
      doneLabel={variant === "page" ? t(($) => $.details.done) : undefined}
      // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell title slot; remount keyed by channel/tab
      leading={leading}
    >
      {/* #820 — cross-fade the panel content on view swap (opacity-only). The
          keyed remount keeps semantics/focus instant and renders only the final
          view on rapid retarget; reduced-motion drops the fade. */}
      <MotionContent motionKey={view} className="flex min-h-0 flex-1 flex-col">
      {view === "home" ? (
        <div
          className="min-h-0 flex-1 space-y-3 overflow-y-auto bg-muted/40 p-3"
          data-testid="channel-details-home"
        >
          <section className="rounded-xl border border-border bg-card p-4">
            <div className="flex items-start gap-3">
              <ChannelDetailsHeroAvatar
                name={channel.name}
                avatarUrl={channel.avatar_url}
                editable={settingsEditable && !!onUpdateAvatar}
                busy={avatarBusy}
                onClick={() => avatarInputRef.current?.click()}
                changeAriaLabel={t(($) => $.details.avatar_change_aria)}
              />
              {settingsEditable && onUpdateAvatar ? (
                <input
                  ref={avatarInputRef}
                  type="file"
                  accept="image/*"
                  className="hidden"
                  aria-label={t(($) => $.details.avatar_change_aria)}
                  onChange={handleAvatarFile}
                />
              ) : null}
              <div className="min-w-0 flex-1">
                {heroEdit === "name" && settingsEditable ? (
                  <div>
                    <Input
                      value={nameDraft}
                      onChange={(e) =>
                        dispatch({ type: "set_name_draft", value: e.target.value })
                      }
                      disabled={renamePending}
                      autoFocus
                      aria-label={t(($) => $.details.rename_label)}
                      data-testid="channel-details-hero-name-input"
                      className="h-9 text-base font-bold tracking-tight"
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          commitName();
                        } else if (e.key === "Escape") {
                          e.preventDefault();
                          cancelName();
                        }
                      }}
                      onBlur={commitName}
                    />
                    <p className="mt-1 text-[10px] text-muted-foreground">
                      {t(($) => $.details.hero_name_hint)}
                    </p>
                  </div>
                ) : settingsEditable ? (
                  <button
                    type="button"
                    data-testid="channel-details-hero-name"
                    className="-mx-1 truncate rounded-md px-1 text-left text-base font-bold tracking-tight hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
                    onClick={() =>
                      dispatch({ type: "begin_name_edit", name: channel.name })
                    }
                  >
                    <span className="text-muted-foreground">#</span>
                    {channel.name}
                  </button>
                ) : (
                  <p
                    data-testid="channel-details-hero-name"
                    className="truncate text-base font-bold tracking-tight"
                  >
                    <span className="text-muted-foreground">#</span>
                    {channel.name}
                  </p>
                )}
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t(($) => $.details.hero_meta, {
                    members: userCount,
                    agents: agentCount,
                  })}
                </p>
                {heroEdit === "description" && settingsEditable && onUpdateDescription ? (
                  <div className="mt-2">
                    <Textarea
                      value={descriptionDraft}
                      onChange={(e) =>
                        dispatch({
                          type: "set_description_draft",
                          value: e.target.value,
                        })
                      }
                      disabled={descriptionPending}
                      autoFocus
                      rows={3}
                      aria-label={t(($) => $.details.description_label)}
                      data-testid="channel-details-hero-description-input"
                      placeholder={t(($) => $.details.add_description)}
                      className="resize-none text-sm"
                      onKeyDown={(e) => {
                        if (e.key === "Escape") {
                          e.preventDefault();
                          cancelDescription();
                        }
                      }}
                      onBlur={commitDescription}
                    />
                    <p className="mt-1 text-[10px] text-muted-foreground">
                      {t(($) => $.details.hero_desc_hint)}
                    </p>
                  </div>
                ) : settingsEditable && onUpdateDescription ? (
                  <button
                    type="button"
                    data-testid="channel-details-hero-description"
                    className="-mx-1.5 mt-2 w-[calc(100%+0.75rem)] rounded-md px-1.5 py-0.5 text-left text-sm leading-5 text-muted-foreground hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    onClick={() =>
                      dispatch({
                        type: "begin_description_edit",
                        description: channel.description ?? "",
                      })
                    }
                  >
                    {channel.description?.trim()
                      ? channel.description
                      : t(($) => $.details.add_description)}
                  </button>
                ) : (
                  <p
                    data-testid="channel-details-hero-description"
                    className="mt-2 text-sm leading-5 text-muted-foreground"
                  >
                    {channel.description?.trim()
                      ? channel.description
                      : t(($) => $.details.add_description)}
                  </p>
                )}
                {settingsEditable ? (
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    {t(($) => $.details.hero_manage_hint)}
                  </p>
                ) : null}
              </div>
            </div>
          </section>

          <button
            type="button"
            onClick={() => dispatch({ type: "set_view", view: "members" })}
            data-testid="channel-details-members-row"
            className="flex w-full items-center gap-3 rounded-xl border border-border bg-card px-3.5 py-3 text-left transition-colors hover:bg-muted/60"
          >
            <ChannelDetailsMemberStack
              members={members}
              overflowText={(count) => t(($) => $.details.member_overflow, { count })}
            />
            <span className="min-w-0 flex-1 text-sm font-semibold">
              {t(($) => $.details.tab_members)}
            </span>
            <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          </button>

          {/* #808 — group-level "no manager yet" onboarding for the OWNER,
              right after the member summary (Iris): zero-manager is a group
              state, so it sits with the group's member entry and its CTA leads
              into Members. Self-gating and fail-closed; renders nothing for
              anyone else. */}
          {channel.kind === "group" ? (
            <GroupManagerHint
              channelId={channel.id}
              onOpenMembers={() => dispatch({ type: "set_view", view: "members" })}
            />
          ) : null}

          <ChannelDetailsSectionCard title={t(($) => $.details.section_notifications)}>
            <ChannelDetailsDetailRow
              icon={<Bell className="size-4" />}
              label={t(($) => $.details.row_notify_pref)}
              value={notifyPrefLabel}
              onClick={
                variant === "page" && onSelectNotifyLevel
                  ? () => dispatch({ type: "set_view", view: "notify-prefs" })
                  : onOpenNotificationPrefs
                    ? () => {
                        onClose();
                        onOpenNotificationPrefs();
                      }
                    : undefined
              }
              testId="channel-details-notify-pref"
            />
            <ChannelDetailsDetailRow
              icon={<VolumeX className="size-4" />}
              label={t(($) => $.details.row_mute)}
              // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Switch is the row control, not a memoized child tree
              trailing={
                <Switch
                  checked={muted}
                  disabled={mutePending}
                  onCheckedChange={() => onMuteToggle()}
                  aria-label={
                    muted
                      ? t(($) => $.sidebar.unmute)
                      : t(($) => $.sidebar.mute)
                  }
                  data-testid="channel-details-mute-switch"
                />
              }
              testId="channel-details-mute"
            />
          </ChannelDetailsSectionCard>

          <ChannelDetailsSectionCard title={t(($) => $.details.section_content)}>
            <ChannelDetailsDetailRow
              icon={<Search className="size-4" />}
              label={t(($) => $.details.row_search)}
              onClick={() => {
                onClose();
                onOpenSearch?.();
              }}
              testId="channel-details-search"
            />
            {/* #821 — Overview Invite removed; adding people is the Members
                sub-page's job (the single roster/Add home). LRM-675 — the
                Files row is gone too: the main-area 「文件」 tab is the
                single entry. */}
          </ChannelDetailsSectionCard>

          {!hideSettingsTab ? (
            <ChannelDetailsSectionCard title={t(($) => $.details.section_system)}>
              <ChannelDetailsDetailRow
                icon={<Settings className="size-4" />}
                label={t(($) => $.details.row_settings)}
                onClick={() => dispatch({ type: "set_view", view: "settings" })}
                testId="channel-details-settings"
              />
            </ChannelDetailsSectionCard>
          ) : null}

          <ChannelDetailsSectionCard>
            {onStopAllAgents ? (
              <ChannelDetailsDetailRow
                icon={<Square className="size-3.5 fill-current" />}
                label={t(($) => $.stop_all_agents.menu_label)}
                onClick={() => {
                  if (stopAllDisabled) return;
                  onClose();
                  onStopAllAgents();
                }}
                disabled={stopAllDisabled}
                destructive
                testId="channel-details-stop-all"
              />
            ) : null}
            {onDelete ? (
              <ChannelDetailsDetailRow
                icon={<Trash2 className="size-4" />}
                label={t(($) => $.details.delete_group)}
                onClick={() => {
                  onClose();
                  onDelete();
                }}
                destructive
                testId="channel-details-delete"
              />
            ) : null}
          </ChannelDetailsSectionCard>

          {stopAllDisabled && stopAllDisabledReason && onStopAllAgents ? (
            <p className="px-1 text-xs text-muted-foreground">{stopAllDisabledReason}</p>
          ) : null}
          {!settingsEditable && manageDisabledReason ? (
            <p className="px-1 text-xs text-muted-foreground">{manageDisabledReason}</p>
          ) : null}
        </div>
      ) : null}

      {view === "members" ? (
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          {membersBody}
        </div>
      ) : null}

      {view === "notify-prefs" && notifyLevel && onSelectNotifyLevel ? (
        <div
          className="min-h-0 flex-1 overflow-y-auto"
          data-testid="channel-details-notify-prefs-view"
        >
          <ChannelNotifyPrefsOptions
            level={notifyLevel}
            pending={notifyLevelPending}
            onSelect={onSelectNotifyLevel}
            onOpenGlobalSettings={onOpenGlobalNotifySettings ?? (() => undefined)}
            density="roomy"
          />
        </div>
      ) : null}

      {view === "settings" && !hideSettingsTab ? (
        // #576 — also the portal-container anchor for the project picker's
        // dropdown (see the `portalContainer` prop doc above); only
        // load-bearing for the mobile `variant="page"` Drawer case.
        <div ref={portalContainer} className="min-h-0 flex-1 overflow-y-auto">
          <div className="space-y-4 border-b p-3 md:p-4">
            {/* #821 / LRM-860 — rename lives on the hero (click-to-edit). */}
            <div>
              <label className="mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                {t(($) => $.details.lark_label)}
              </label>
              <div className="flex gap-2">
                <Input
                  value={larkDraft}
                  onChange={(e) =>
                    dispatch({ type: "set_lark_draft", value: e.target.value })
                  }
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

          {/* Archive stays in Settings (LRM-494 danger card is Stop + Delete only). */}
          <div className="border-t p-3 md:p-4">
            <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
              {t(($) => $.details.danger_zone)}
            </p>
            <div className="divide-y">
              <div className="py-2.5">
                {canManage && !isArchived ? (
                  <button
                    type="button"
                    onClick={onArchive}
                    className="w-full text-left hover:opacity-80"
                  >
                    <p className="text-sm font-semibold text-ink">
                      {t(($) => $.details.archive_title)}
                    </p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {t(($) => $.details.archive_description)}
                    </p>
                  </button>
                ) : (
                  <div>
                    <p className="text-sm font-semibold text-ink opacity-50">
                      {t(($) => $.details.archive_title)}
                    </p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {isArchived
                        ? t(($) => $.details.archive_disabled_archived)
                        : manageDisabledReason ??
                          t(($) => $.sidebar.archive_permission)}
                    </p>
                  </div>
                )}
              </div>
              {groupLeave ? (
                <div className="py-2.5" data-testid="channel-details-leave">
                  {groupLeave.onLeave ? (
                    <button
                      type="button"
                      onClick={groupLeave.onLeave}
                      className="w-full text-left hover:opacity-80"
                    >
                      <p className="text-sm font-semibold text-destructive">
                        {t(($) => $.details.leave_group)}
                      </p>
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        {t(($) => $.details.leave_description)}
                      </p>
                    </button>
                  ) : (
                    <div>
                      <p className="text-sm font-semibold text-ink opacity-50">
                        {t(($) => $.details.leave_group)}
                      </p>
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        {groupLeave.disabledReason}
                      </p>
                    </div>
                  )}
                </div>
              ) : null}
            </div>
          </div>
          {/* Keep projectBound referenced so callers' subtitle contract stays used. */}
          <span className="sr-only">
            {projectBound
              ? t(($) => $.details.project_bound)
              : t(($) => $.details.project_unbound)}
          </span>
        </div>
      ) : null}
      </MotionContent>
    </ConversationSidePanelShell>
  );
}
