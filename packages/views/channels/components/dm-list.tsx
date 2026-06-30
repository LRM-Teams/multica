"use client";

import { useMemo, useState } from "react";
import { Bell, BellOff, ChevronDown, ChevronRight, Loader2, Mail, MoreHorizontal, Pin, PinOff, Plus, Search, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  dmListOptions,
  useSetDMPinned,
  useMarkDMUnread,
  useCloseDM,
  useMuteDM,
} from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@multica/ui/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
} from "@multica/ui/components/ui/drawer";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import {
  matchesActorIdentitySearch,
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { useT, useTimeAgo } from "../../i18n";
import { useOpenDM } from "../../common/use-open-dm";
import { formatChannelMessagePreview, type MentionPreviewResolver } from "./message-preview";
import {
  ConversationUnreadAffordance,
  isConversationMuted,
  MutedIndicator,
  sumUnmutedUnreadCounts,
} from "./conversation-muted";

const identitySearchOptions = { extendedMatch: matchesPinyin };

const newDmTriggerCls =
  "flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground";

/**
 * DIRECT MESSAGES sidebar region — the top half of the unified Messages
 * sidebar (GROUPS sits below). Fed by `GET /api/dm`, which unions kind='dm'
 * channels with legacy chat_sessions and is already recency-sorted, so we
 * preserve the server order (unread / new float up). The header is collapsible
 * and, when collapsed, surfaces the aggregate unread count.
 *
 * Selection is unified with groups by the parent: `activeId` is the currently
 * open conversation id regardless of region, so opening a DM clears the group
 * selection and vice-versa.
 */
export function DmList({
  activeId,
  currentUserName,
  searchQuery = "",
  onSelect,
}: {
  /** Currently open conversation id (DM or group) — drives row highlight. */
  activeId: string | null;
  /** Viewer's display name, used to detect mentions in the last-message preview. */
  currentUserName: string | null;
  /** Parent Messages sidebar query. Filters conversations only, never message bodies. */
  searchQuery?: string;
  onSelect: (dm: DMItem) => void;
}) {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const isMobile = useIsMobile();
  const wsId = useWorkspaceId();
  const { data: dms = [], isLoading } = useQuery(dmListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [collapsed, setCollapsed] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);

  const setPinned = useSetDMPinned();
  const markUnread = useMarkDMUnread();
  const closeDM = useCloseDM();
  const muteDM = useMuteDM();

  const onError = () => toast.error(t(($) => $.dm.action_failed));
  const handleTogglePin = (dm: DMItem) =>
    setPinned.mutate({ source: dm.source, id: dm.id, pinned: !dm.pinned_at }, { onError });
  const handleMarkUnread = (dm: DMItem) =>
    markUnread.mutate({ source: dm.source, id: dm.id }, { onError });
  const handleClose = (dm: DMItem) =>
    closeDM.mutate({ source: dm.source, id: dm.id }, { onError });
  const handleToggleMute = (dm: DMItem) =>
    muteDM.mutate({ source: dm.source, id: dm.id, muted: !isConversationMuted(dm) }, { onError });

  const aggregateUnread = useMemo(
    () =>
      sumUnmutedUnreadCounts(
        dms,
        (dm) => dm.real_unread ?? dm.unread ?? 0,
        (dm) => isConversationMuted(dm),
      ),
    [dms],
  );

  // Pinned conversations float to the top of DIRECT MESSAGES; the server keeps
  // each group recency-sorted, so a stable sort preserves that order within the
  // pinned and unpinned groups.
  const sortedDms = useMemo(
    () => [...dms].sort((a, b) => (b.pinned_at ? 1 : 0) - (a.pinned_at ? 1 : 0)),
    [dms],
  );
  const resolveMentionPreview = useMemo<MentionPreviewResolver>(
    () => (type, id, fallbackLabel) => {
      if (type === "agent") {
        const agent = agents.find((a) => a.id === id);
        return agent?.display_name?.trim() || agent?.name?.trim() || fallbackLabel;
      }
      const member = members.find((m) => m.user_id === id);
      return member?.display_name?.trim() || member?.name?.trim() || fallbackLabel;
    },
    [agents, members],
  );
  const filteredDms = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return sortedDms;
    return sortedDms.filter((dm) => dm.peer.name.toLowerCase().includes(q));
  }, [searchQuery, sortedDms]);

  const showHeaderTrigger = isLoading || dms.length > 0;
  const openPicker = () => setPickerOpen(true);
  const closePicker = () => setPickerOpen(false);

  const listBody =
    !collapsed &&
    (isLoading ? (
      <div className="space-y-2 p-2">
        <Skeleton className="h-12" />
        <Skeleton className="h-12" />
      </div>
    ) : dms.length === 0 ? (
      <div className="flex flex-col items-center gap-2 px-3 py-3">
        <p className="text-xs text-muted-foreground">{t(($) => $.dm.empty)}</p>
        {isMobile ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="w-full text-xs"
            onClick={openPicker}
          >
            <Plus className="size-3.5" />
            {t(($) => $.dm.empty_cta)}
          </Button>
        ) : (
          <PopoverTrigger
            render={
              <Button type="button" variant="outline" size="sm" className="w-full text-xs" />
            }
          >
            <Plus className="size-3.5" />
            {t(($) => $.dm.empty_cta)}
          </PopoverTrigger>
        )}
      </div>
    ) : filteredDms.length === 0 ? (
      <div className="space-y-1 px-3 py-4 text-xs text-muted-foreground">
        <p className="font-medium text-foreground">
          {t(($) => $.sidebar.no_conversation_matches)}
        </p>
        <p>{t(($) => $.sidebar.search_scope_hint)}</p>
      </div>
    ) : (
      filteredDms.map((dm) => (
        <DmRow
          key={`${dm.source}:${dm.id}`}
          dm={dm}
          active={activeId === dm.id}
          currentUserName={currentUserName}
          timeAgo={timeAgo}
          resolveMentionPreview={resolveMentionPreview}
          onSelect={() => onSelect(dm)}
          onTogglePin={() => handleTogglePin(dm)}
          onMarkUnread={() => handleMarkUnread(dm)}
          onToggleMute={() => handleToggleMute(dm)}
          onClose={() => handleClose(dm)}
        />
      ))
    ));

  return (
    <div className="pb-1">
      <Popover
        open={isMobile ? false : pickerOpen}
        onOpenChange={isMobile ? undefined : setPickerOpen}
      >
        {/* Header row: collapse toggle (flex-1) + "+" new DM button */}
        <div className="flex items-center gap-0.5 px-2 py-1.5">
          <button
            type="button"
            onClick={() => setCollapsed((c) => !c)}
            className="flex flex-1 items-center gap-1 text-xs font-medium uppercase tracking-wide text-muted-foreground transition-colors hover:text-foreground"
            aria-expanded={!collapsed}
          >
            {collapsed ? (
              <ChevronRight className="size-3.5 shrink-0" />
            ) : (
              <ChevronDown className="size-3.5 shrink-0" />
            )}
            <span className="flex-1 text-left">{t(($) => $.dm.heading)}</span>
            {collapsed && aggregateUnread > 0 && (
              <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
                {aggregateUnread > 99 ? "99+" : aggregateUnread}
              </span>
            )}
          </button>

          {showHeaderTrigger &&
            (isMobile ? (
              <button
                type="button"
                aria-label={t(($) => $.dm.new_aria)}
                onClick={openPicker}
                className={newDmTriggerCls}
              >
                <Plus className="size-4" />
              </button>
            ) : (
              <PopoverTrigger
                render={
                  <button type="button" aria-label={t(($) => $.dm.new_aria)} className={newDmTriggerCls} />
                }
              >
                <Plus className="size-4" />
              </PopoverTrigger>
            ))}
        </div>

        {listBody}

        {!isMobile && (
          <PopoverContent align="start" className="w-72 p-0">
            <div className="border-b px-3 py-2.5">
              <p className="text-sm font-medium">{t(($) => $.dm.new_title)}</p>
            </div>
            <DmPickerContent onClose={closePicker} />
          </PopoverContent>
        )}
      </Popover>

      {isMobile && (
        <Drawer open={pickerOpen} onOpenChange={setPickerOpen}>
          <DrawerContent className="px-4 pb-8">
            <DrawerHeader className="px-0 pb-3">
              <DrawerTitle>{t(($) => $.dm.new_title)}</DrawerTitle>
            </DrawerHeader>
            <DmPickerContent onClose={closePicker} />
          </DrawerContent>
        </Drawer>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Picker body — search input + results list (agents + members)
// ---------------------------------------------------------------------------

function DmPickerContent({ onClose }: { onClose: () => void }) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);
  const { openDM, isPending } = useOpenDM();
  const [search, setSearch] = useState("");

  const { data: agents = [], isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId),
  );
  const { data: members = [], isLoading: membersLoading } = useQuery(
    memberListOptions(wsId),
  );

  const items = useMemo(() => {
    const q = search.trim();
    const agentItems = agents
      .filter((a) => !a.archived_at)
      .map((a) => ({
        kind: "agent" as const,
        id: a.id,
        presentation: resolveActorIdentityPresentation(a, a.id),
      }));
    const memberItems = members
      .filter((m) => m.user_id !== currentUser?.id)
      .map((m) => ({
        kind: "user" as const,
        id: m.user_id,
        presentation: resolveActorIdentityPresentation(m, m.user_id),
      }));
    const all: Array<{
      kind: "agent" | "user";
      id: string;
      presentation: ActorIdentityPresentation;
    }> = [...agentItems, ...memberItems];
    if (!q) return all;
    return all.filter((item) =>
      matchesActorIdentitySearch(
        item.presentation.displayName,
        item.presentation.handle,
        q,
        identitySearchOptions,
      ),
    );
  }, [agents, members, currentUser?.id, search]);

  const isLoading = agentsLoading || membersLoading;

  const handleSelect = async (item: { kind: "agent" | "user"; id: string }) => {
    await openDM({ peer_type: item.kind, peer_id: item.id });
    onClose();
  };

  return (
    <div className="flex flex-col gap-1 p-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t(($) => $.dm.search_placeholder)}
          className="h-8 pl-8 text-sm"
          autoFocus
        />
      </div>
      <div className="mt-1 max-h-60 overflow-y-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-6">
            <Loader2 className="size-4 animate-spin text-muted-foreground" />
          </div>
        ) : items.length === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            {t(($) => $.dm.no_results)}
          </p>
        ) : (
          items.map((item) => (
            <button
              key={`${item.kind}:${item.id}`}
              type="button"
              disabled={isPending}
              onClick={() => void handleSelect(item)}
              className="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-accent disabled:opacity-50"
            >
              <ActorAvatar
                actorType={item.kind === "agent" ? "agent" : "member"}
                actorId={item.id}
                size={28}
                showStatusDot={item.kind === "agent"}
                profileLink={false}
              />
              <ActorIdentityRow
                displayName={item.presentation.displayName}
                handle={item.presentation.handle}
                showHandle={item.presentation.showHandleLabel}
                primaryClassName="truncate text-sm font-medium text-foreground"
              />
            </button>
          ))
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// DM list row
// ---------------------------------------------------------------------------

function DmRow({
  dm,
  active,
  currentUserName,
  timeAgo,
  resolveMentionPreview,
  onSelect,
  onTogglePin,
  onMarkUnread,
  onToggleMute,
  onClose,
}: {
  dm: DMItem;
  active: boolean;
  currentUserName: string | null;
  timeAgo: (dateStr: string) => string;
  resolveMentionPreview: MentionPreviewResolver;
  onSelect: () => void;
  /** Pin / unpin (toggles based on current pinned state). */
  onTogglePin: () => void;
  /** Mark the conversation manually unread. */
  onMarkUnread: () => void;
  /** Mute / unmute (toggles based on current muted state). */
  onToggleMute: () => void;
  /** Close Chat — soft-hide the conversation (recoverable). */
  onClose: () => void;
}) {
  const { t } = useT("channels");
  const last = dm.last_message;
  const preview = last
    ? formatChannelMessagePreview(last.author_name, last.content, resolveMentionPreview)
    : "";
  // Surface mentions of the viewer at full foreground weight (no bold) so an
  // @-mention reads as more salient than ordinary preview text.
  const mentionsUser =
    !!last &&
    !!currentUserName &&
    last.content.toLowerCase().includes(`@${currentUserName.toLowerCase()}`);
  const realUnread = dm.real_unread ?? dm.unread ?? 0;
  const isManualDot = !!dm.manually_unread && realUnread === 0;
  const pinned = !!dm.pinned_at;
  const isMuted = isConversationMuted(dm);
  // peer.type "user" maps to the member-style avatar; agents get the presence
  // status dot. Both resolve name/avatar from the workspace queries.
  const actorType = dm.peer.type === "agent" ? "agent" : "member";

  return (
    <ContextMenu>
      <ContextMenuTrigger
        render={
          <div
            className={cn(
              "group/row relative mb-0.5 rounded-lg transition-colors",
              active ? "bg-primary/[0.08]" : "hover:bg-accent",
            )}
          />
        }
      >
        <button
          type="button"
          onClick={onSelect}
          className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left"
        >
          <ActorAvatar
            actorType={actorType}
            actorId={dm.peer.id}
            size={40}
            showStatusDot={dm.peer.type === "agent"}
            profileLink={false}
          />
          <div className="min-w-0 flex-1">
            <div className="flex items-center justify-between gap-2">
              <span className="flex min-w-0 items-center gap-1">
                {pinned && (
                  <Pin className="size-3 shrink-0 -rotate-45 fill-muted-foreground/70 text-muted-foreground/70" />
                )}
                <span className="truncate text-sm font-medium text-foreground">
                  {dm.peer.name}
                </span>
                {isMuted && <MutedIndicator label={t(($) => $.dm.muted_label)} />}
              </span>
              {last && (
                <span className="shrink-0 text-[11px] text-muted-foreground">
                  {timeAgo(last.created_at)}
                </span>
              )}
            </div>
            <div className="mt-0.5 flex items-center justify-between gap-2">
              <span
                className={cn(
                  "truncate text-xs",
                  mentionsUser ? "text-foreground" : "text-muted-foreground",
                )}
              >
                {preview}
              </span>
              <ConversationUnreadAffordance
                realUnread={realUnread}
                isManualDot={isManualDot}
                isMuted={isMuted}
              />
            </div>
          </div>
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <button
                type="button"
                aria-label={t(($) => $.dm.menu_aria)}
                className="absolute right-1 top-1.5 flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus-visible:opacity-100 group-hover/row:opacity-100 data-[popup-open]:opacity-100"
              >
                <MoreHorizontal className="size-4" />
              </button>
            }
          />
          <DropdownMenuContent align="end">
            <DmDropdownMenuItems
              pinned={pinned}
              isMuted={isMuted}
              onMarkUnread={onMarkUnread}
              onTogglePin={onTogglePin}
              onToggleMute={onToggleMute}
              onClose={onClose}
            />
          </DropdownMenuContent>
        </DropdownMenu>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <DmContextMenuItems
          pinned={pinned}
          isMuted={isMuted}
          onMarkUnread={onMarkUnread}
          onTogglePin={onTogglePin}
          onToggleMute={onToggleMute}
          onClose={onClose}
        />
      </ContextMenuContent>
    </ContextMenu>
  );
}

function DmContextMenuItems({
  pinned,
  isMuted,
  onMarkUnread,
  onTogglePin,
  onToggleMute,
  onClose,
}: {
  pinned: boolean;
  isMuted: boolean;
  onMarkUnread: () => void;
  onTogglePin: () => void;
  onToggleMute: () => void;
  onClose: () => void;
}) {
  const { t } = useT("channels");
  return (
    <>
      <ContextMenuItem onClick={onMarkUnread}>
        <Mail />
        {t(($) => $.dm.mark_unread)}
      </ContextMenuItem>
      <ContextMenuItem onClick={onTogglePin}>
        {pinned ? <PinOff /> : <Pin />}
        {pinned ? t(($) => $.dm.unpin) : t(($) => $.dm.pin)}
      </ContextMenuItem>
      <ContextMenuItem onClick={onToggleMute}>
        {isMuted ? <Bell /> : <BellOff />}
        {isMuted ? t(($) => $.dm.unmute) : t(($) => $.dm.mute)}
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem onClick={onClose}>
        <X />
        {t(($) => $.dm.close_chat)}
      </ContextMenuItem>
    </>
  );
}

function DmDropdownMenuItems({
  pinned,
  isMuted,
  onMarkUnread,
  onTogglePin,
  onToggleMute,
  onClose,
}: {
  pinned: boolean;
  isMuted: boolean;
  onMarkUnread: () => void;
  onTogglePin: () => void;
  onToggleMute: () => void;
  onClose: () => void;
}) {
  const { t } = useT("channels");
  return (
    <>
      <DropdownMenuItem onClick={onMarkUnread}>
        <Mail className="size-4" />
        {t(($) => $.dm.mark_unread)}
      </DropdownMenuItem>
      <DropdownMenuItem onClick={onTogglePin}>
        {pinned ? <PinOff className="size-4" /> : <Pin className="size-4" />}
        {pinned ? t(($) => $.dm.unpin) : t(($) => $.dm.pin)}
      </DropdownMenuItem>
      <DropdownMenuItem onClick={onToggleMute}>
        {isMuted ? <Bell className="size-4" /> : <BellOff className="size-4" />}
        {isMuted ? t(($) => $.dm.unmute) : t(($) => $.dm.mute)}
      </DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem onClick={onClose}>
        <X className="size-4" />
        {t(($) => $.dm.close_chat)}
      </DropdownMenuItem>
    </>
  );
}
