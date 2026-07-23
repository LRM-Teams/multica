"use client";

import { type ReactNode, type RefObject, useState } from "react";
import {
  Bell,
  ChevronLeft,
  ChevronRight,
  FileText,
  ImageIcon,
  Search,
  Settings,
  Square,
  Tag,
  Trash2,
  UserPlus,
  VolumeX,
} from "lucide-react";
import type { Channel, ChannelMemberBrief } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { useT } from "../../i18n";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";
import { isConversationMuted } from "./conversation-muted";

/**
 * LRM-494 — Slack-style channel details surface.
 * Home is a single overview (hero + section cards + danger). Drill-down
 * reuses the prior About/Members/Files/Settings bodies as sub-views.
 * `initialTab` still remounts into a drill-down when the opener requests it.
 */
export type ChannelDetailsTab = "about" | "members" | "files" | "settings";
type DetailsView = "home" | ChannelDetailsTab | "about-edit" | "avatar";

const MEMBER_STACK_MAX = 5;

function resolveInitialView(
  initialTab: ChannelDetailsTab,
  hideSettingsTab: boolean,
): DetailsView {
  if (hideSettingsTab && initialTab === "settings") return "home";
  if (initialTab === "about") return "home";
  return initialTab;
}

function ChannelHeroAvatar({ name }: { name: string }) {
  const glyph = (name.trim().charAt(0) || "#").toUpperCase();
  return (
    <span
      data-testid="channel-details-hero-avatar"
      className="flex size-16 shrink-0 items-center justify-center rounded-full border border-border bg-muted text-xl font-bold text-foreground"
      aria-hidden="true"
    >
      {glyph}
    </span>
  );
}

function MemberAvatarStack({
  members,
  overflowText,
}: {
  members: ChannelMemberBrief[];
  overflowText: (count: number) => string;
}) {
  const visible = members.slice(0, MEMBER_STACK_MAX);
  const overflow = Math.max(0, members.length - visible.length);
  const overlap = 10;
  return (
    <span className="inline-flex items-center" data-testid="channel-details-member-stack">
      {visible.map((m, i) => (
        <span
          key={`${m.member_type}:${m.member_id}`}
          style={{ marginLeft: i === 0 ? 0 : -overlap }}
          className="inline-flex rounded-full ring-2 ring-card"
        >
          <ActorAvatar
            actorType={m.member_type === "agent" ? "agent" : "member"}
            actorId={m.member_id}
            size={28}
            avatarUrlHint={m.avatar_url}
            profileLink={false}
          />
        </span>
      ))}
      {overflow > 0 ? (
        <span
          style={{ marginLeft: -overlap }}
          className="inline-flex size-7 items-center justify-center rounded-full bg-muted text-[10px] font-semibold text-muted-foreground ring-2 ring-card"
        >
          {overflowText(overflow)}
        </span>
      ) : null}
    </span>
  );
}

function SectionCard({
  title,
  children,
}: {
  title?: string;
  children: ReactNode;
}) {
  return (
    <section className="overflow-hidden rounded-xl border border-border bg-card">
      {title ? (
        <p className="border-b border-border px-3.5 py-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {title}
        </p>
      ) : null}
      <div className="divide-y divide-border">{children}</div>
    </section>
  );
}

function DetailRow({
  icon,
  label,
  value,
  onClick,
  disabled,
  trailing,
  destructive,
  testId,
}: {
  icon: ReactNode;
  label: string;
  value?: string;
  onClick?: () => void;
  disabled?: boolean;
  trailing?: ReactNode;
  destructive?: boolean;
  testId?: string;
}) {
  const content = (
    <>
      <span
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground",
          destructive && "bg-destructive/10 text-destructive",
          disabled && "opacity-50",
        )}
      >
        {icon}
      </span>
      <span className="min-w-0 flex-1 text-left">
        <span
          className={cn(
            "block truncate text-sm font-medium",
            destructive ? "text-destructive" : "text-foreground",
            disabled && "text-muted-foreground",
          )}
        >
          {label}
        </span>
      </span>
      {value ? (
        <span className="shrink-0 text-sm text-muted-foreground">{value}</span>
      ) : null}
      {trailing}
      {onClick && !trailing ? (
        <ChevronRight
          className={cn("size-4 shrink-0 text-muted-foreground", disabled && "opacity-40")}
          aria-hidden="true"
        />
      ) : null}
    </>
  );

  if (!onClick) {
    return (
      <div
        data-testid={testId}
        className="flex min-h-11 items-center gap-3 px-3.5 py-2.5"
      >
        {content}
      </div>
    );
  }

  return (
    <button
      type="button"
      data-testid={testId}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex min-h-11 w-full items-center gap-3 px-3.5 py-2.5 text-left transition-colors",
        disabled ? "cursor-not-allowed opacity-60" : "hover:bg-muted/60",
      )}
    >
      {content}
    </button>
  );
}

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
  onShare: _onShare,
  onArchive,
  onDelete,
  onRename,
  renamePending,
  onUpdateDescription,
  descriptionPending,
  onUpdateLarkChatId,
  larkPending,
  membersBody,
  initialTab = "about",
  hideSettingsTab = false,
  onClose,
  variant = "panel",
  portalContainer,
  onOpenSearch,
  onInvite,
  onStopAllAgents,
  stopAllDisabled,
  stopAllDisabledReason,
  canInvite = false,
  notifyPrefLabel,
  onOpenNotificationPrefs,
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
  /**
   * LRM-239 — permanent delete (owner/admin only). Omit to hide the delete
   * entry entirely (members / creator-members / system surfaces).
   */
  onDelete?: () => void;
  onRename: (name: string) => void;
  renamePending?: boolean;
  onUpdateDescription?: (description: string | null) => void;
  descriptionPending?: boolean;
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
   * panel is hosted inside the mobile page Drawer (`variant="page"`).
   */
  portalContainer?: RefObject<HTMLDivElement | null>;
  /** LRM-494 — channel-scoped search (not global). */
  onOpenSearch?: () => void;
  onInvite?: () => void;
  onStopAllAgents?: () => void;
  stopAllDisabled?: boolean;
  stopAllDisabledReason?: string;
  canInvite?: boolean;
  /** LRM-494 — live preference label from workspace notify settings (LRM-414). */
  notifyPrefLabel: string;
  onOpenNotificationPrefs?: () => void;
}) {
  const { t } = useT("channels");

  // Local UI state only — parent remounts this panel via key when channel/tab
  // opener changes, so these are not kept in sync through effects.
  // react-doctor-disable-next-line react-doctor/no-derived-state -- intentional editable draft + active view; remount keyed by channel/initialTab
  const [view, setView] = useState<DetailsView>(() =>
    resolveInitialView(initialTab, hideSettingsTab),
  );
  // react-doctor-disable-next-line react-doctor/no-derived-useState, react-doctor/no-derived-state -- rename/lark drafts; remount keyed by channel.id
  const [nameDraft, setNameDraft] = useState(channel.name);
  // react-doctor-disable-next-line react-doctor/no-derived-useState, react-doctor/no-derived-state -- rename/lark drafts; remount keyed by channel.id
  const [descriptionDraft, setDescriptionDraft] = useState(channel.description ?? "");
  // react-doctor-disable-next-line react-doctor/no-derived-useState, react-doctor/no-derived-state -- rename/lark drafts; remount keyed by channel.id
  const [larkDraft, setLarkDraft] = useState(channel.lark_chat_id ?? "");

  const muted = isConversationMuted(channel);
  const nameDirty = nameDraft.trim() !== channel.name;
  const descriptionDirty =
    (descriptionDraft.trim() || null) !== (channel.description?.trim() || null);
  const larkDirty = (larkDraft.trim() || null) !== (channel.lark_chat_id || null);
  const settingsEditable = canManage && !isArchived;

  const userCount = members.filter((m) => m.member_type === "user").length;
  const agentCount = members.filter((m) => m.member_type === "agent").length;

  const goHome = () => setView("home");

  const subTitle =
    view === "members"
      ? t(($) => $.details.tab_members)
      : view === "files"
        ? t(($) => $.details.tab_files)
        : view === "settings"
          ? t(($) => $.details.section_settings)
          : view === "about-edit"
            ? t(($) => $.details.row_name_description)
            : view === "avatar"
              ? t(($) => $.details.row_avatar)
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
      // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- shell leading slot; remount keyed by channel/tab
      leading={leading}
    >
      {view === "home" ? (
        <div
          className="min-h-0 flex-1 space-y-3 overflow-y-auto bg-muted/40 p-3"
          data-testid="channel-details-home"
        >
          <section className="rounded-xl border border-border bg-card p-4">
            <div className="flex items-start gap-3">
              <ChannelHeroAvatar name={channel.name} />
              <div className="min-w-0 flex-1">
                <p className="truncate text-base font-bold tracking-tight">
                  <span className="text-muted-foreground">#</span>
                  {channel.name}
                </p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t(($) => $.details.hero_meta, {
                    members: userCount,
                    agents: agentCount,
                  })}
                </p>
                <p className="mt-2 text-sm leading-5 text-muted-foreground">
                  {channel.description?.trim()
                    ? channel.description
                    : t(($) => $.details.add_description)}
                </p>
              </div>
            </div>
          </section>

          <button
            type="button"
            onClick={() => setView("members")}
            data-testid="channel-details-members-row"
            className="flex w-full items-center gap-3 rounded-xl border border-border bg-card px-3.5 py-3 text-left transition-colors hover:bg-muted/60"
          >
            <MemberAvatarStack
              members={members}
              overflowText={(count) => t(($) => $.details.member_overflow, { count })}
            />
            <span className="min-w-0 flex-1 text-sm font-semibold">
              {t(($) => $.details.tab_members)}
            </span>
            <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          </button>

          <SectionCard title={t(($) => $.details.section_about)}>
            <DetailRow
              icon={<Tag className="size-4" />}
              label={t(($) => $.details.row_name_description)}
              onClick={() => setView("about-edit")}
              disabled={!settingsEditable}
              testId="channel-details-about-name"
            />
            <DetailRow
              icon={<ImageIcon className="size-4" />}
              label={t(($) => $.details.row_avatar)}
              onClick={() => setView("avatar")}
              disabled={!settingsEditable}
              testId="channel-details-about-avatar"
            />
          </SectionCard>

          <SectionCard title={t(($) => $.details.section_notifications)}>
            <DetailRow
              icon={<Bell className="size-4" />}
              label={t(($) => $.details.row_notify_pref)}
              value={notifyPrefLabel}
              onClick={
                onOpenNotificationPrefs
                  ? () => {
                      onClose();
                      onOpenNotificationPrefs();
                    }
                  : undefined
              }
              testId="channel-details-notify-pref"
            />
            <DetailRow
              icon={<VolumeX className="size-4" />}
              label={t(($) => $.details.row_mute)}
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
          </SectionCard>

          <SectionCard title={t(($) => $.details.section_content)}>
            <DetailRow
              icon={<Search className="size-4" />}
              label={t(($) => $.details.row_search)}
              onClick={() => {
                onClose();
                onOpenSearch?.();
              }}
              testId="channel-details-search"
            />
            <DetailRow
              icon={<FileText className="size-4" />}
              label={t(($) => $.details.row_files)}
              onClick={() => setView("files")}
              testId="channel-details-files"
            />
            <DetailRow
              icon={<UserPlus className="size-4" />}
              label={t(($) => $.details.row_invite)}
              onClick={() => {
                if (!canInvite || !onInvite) return;
                onClose();
                onInvite();
              }}
              disabled={!canInvite || !onInvite}
              testId="channel-details-invite"
            />
          </SectionCard>

          {!hideSettingsTab ? (
            <SectionCard title={t(($) => $.details.section_system)}>
              <DetailRow
                icon={<Settings className="size-4" />}
                label={t(($) => $.details.row_settings)}
                onClick={() => setView("settings")}
                testId="channel-details-settings"
              />
            </SectionCard>
          ) : null}

          <SectionCard>
            {onStopAllAgents ? (
              <DetailRow
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
              <DetailRow
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
          </SectionCard>

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

      {view === "files" ? (
        <div className="min-h-0 flex-1 overflow-y-auto p-3">
          <ChannelFilesPanel channelId={channel.id} />
        </div>
      ) : null}

      {view === "about-edit" ? (
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
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
          </div>
          <div>
            <label className="mb-1.5 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
              {t(($) => $.details.description_label)}
            </label>
            <Textarea
              value={descriptionDraft}
              onChange={(e) => setDescriptionDraft(e.target.value)}
              disabled={!settingsEditable || descriptionPending || !onUpdateDescription}
              aria-label={t(($) => $.details.description_label)}
              rows={4}
              placeholder={t(($) => $.details.add_description)}
              className="resize-none text-sm"
            />
            <div className="mt-2 flex justify-end">
              <Button
                type="button"
                size="sm"
                disabled={
                  !settingsEditable ||
                  !onUpdateDescription ||
                  !descriptionDirty ||
                  descriptionPending
                }
                onClick={() =>
                  onUpdateDescription?.(descriptionDraft.trim() || null)
                }
              >
                {t(($) => $.details.save)}
              </Button>
            </div>
          </div>
          {!settingsEditable && manageDisabledReason ? (
            <p className="text-xs text-muted-foreground">{manageDisabledReason}</p>
          ) : null}
        </div>
      ) : null}

      {view === "avatar" ? (
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
          <div className="flex flex-col items-center gap-3 rounded-xl border border-border bg-card p-6">
            <ChannelHeroAvatar name={channel.name} />
            <p className="text-center text-sm text-muted-foreground">
              {t(($) => $.details.avatar_pending)}
            </p>
          </div>
          {!settingsEditable && manageDisabledReason ? (
            <p className="text-xs text-muted-foreground">{manageDisabledReason}</p>
          ) : null}
        </div>
      ) : null}

      {view === "settings" && !hideSettingsTab ? (
        // #576 — also the portal-container anchor for the project picker's
        // dropdown (see the `portalContainer` prop doc above); only
        // load-bearing for the mobile `variant="page"` Drawer case.
        <div ref={portalContainer} className="min-h-0 flex-1 overflow-y-auto">
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
    </ConversationSidePanelShell>
  );
}
