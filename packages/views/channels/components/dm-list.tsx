"use client";

import { useMemo, useState } from "react";
import { Bell, BellOff, ChevronDown, ChevronRight, Loader2, Mail, MoreHorizontal, Pin, PinOff, Plus, Search, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { showErrorToast } from "@multica/ui/lib/error-toast";
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
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import {
  computeDuplicatedHandleLabels,
  matchesActorIdentitySearch,
  resolveActorDisplayName,
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { ActorStyledName } from "../../common/actor-styled-name";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { useT } from "../../i18n/use-t";
import { Time } from "../../i18n/time";
import { useOpenDM } from "../../common/use-open-dm";
import {
  dmAgentBubbleActivity,
  useAgentBubbleActivityByAgent,
} from "../../chat/lib/agent-bubble-unread";
import {
  formatChannelMessagePreview,
  resolveChannelAuthorDisplayName,
  type MentionPreviewResolver,
} from "./message-preview";
import { formatSystemEventPreviewText } from "./channel-system-event-preview-text";
import {
  ConversationUnreadAffordance,
  isConversationMuted,
  MutedIndicator,
  sumUnmutedUnreadCounts,
} from "./conversation-muted";
import { DmListSkeleton } from "./conversation-sidebar-list-skeleton";
import {
  CONVERSATION_SIDEBAR_ROW_ACTIVE,
  CONVERSATION_SIDEBAR_ROW_IDLE,
  CONVERSATION_SIDEBAR_UNREAD_BADGE,
} from "./conversation-sidebar-styles";
import { useSidebarSectionCollapsed } from "../hooks/use-sidebar-section-collapsed";

const identitySearchOptions = { extendedMatch: matchesPinyin };

const newDmTriggerCls =
  "flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground";

/**
 * DIRECT MESSAGES sidebar region. Fed by `GET /api/dm`; the visible R2 surface
 * is `dm_channel` only. The header is collapsible and, when collapsed, surfaces
 * the aggregate unread count for *unpinned* DMs (pinned ones live in the unified
 * PINNED section above — Slack-style, not float-to-top within this list).
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
  const isMobile = useIsMobile();
  const wsId = useWorkspaceId();
  // LRM-459: gate on isPending so disabled / pre-fetch idle never paints empty
  // CTA (isLoading is false when isPending && !isFetching).
  const { data: dms = [], isPending: dmsPending } = useQuery(dmListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const bubbleActivityByAgent = useAgentBubbleActivityByAgent(wsId);
  // LRM-655: persist collapse across ChannelsPage/DmList remounts (e.g. select channel).
  const [collapsed, setCollapsed] = useSidebarSectionCollapsed("dms", wsId);
  const [agentDmsCollapsed, setAgentDmsCollapsed] = useSidebarSectionCollapsed(
    "agent-dms",
    wsId,
    true,
  );
  const [pickerOpen, setPickerOpen] = useState(false);

  const dmActions = useDmRowActions();

  // Pinned DMs belong in the unified PINNED section (parent), not here.
  const unpinnedDms = useMemo(() => dms.filter((dm) => !dm.pinned_at), [dms]);

  const aggregateUnread = useMemo(
    () =>
      sumUnmutedUnreadCounts(
        unpinnedDms,
        (dm) => {
          const channelUnread = dm.real_unread ?? dm.unread ?? 0;
          const bubble = dmAgentBubbleActivity(dm, bubbleActivityByAgent);
          return channelUnread + (bubble?.unreadCount ?? 0);
        },
        (dm) => isConversationMuted(dm),
      ),
    [unpinnedDms, bubbleActivityByAgent],
  );

  // Server already returns recency order; do not float pinned items — they live
  // in the unified PINNED section above (Slack Starred / 置顶分组 semantics).
  const resolveMentionPreview = useMemo<MentionPreviewResolver>(
    () => (type, id, fallbackLabel) => {
      if (type === "agent") {
        const agent = agents.find((a) => a.id === id);
        return resolveActorDisplayName(agent, fallbackLabel);
      }
      const member = members.find((m) => m.user_id === id);
      return resolveActorDisplayName(member, fallbackLabel);
    },
    [agents, members],
  );
  const filteredDms = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    const base = q
      ? unpinnedDms.filter((dm) => dm.peer.name.toLowerCase().includes(q))
      : unpinnedDms;
    // Bubble replies do not update dm_channel.updated_at. Sort agent rows by
    // the newest independent bubble session activity too, so a row that was
    // bumped by a bubble reply does not fall back down the moment opening the
    // bubble clears has_unread.
    return base.toSorted((a, b) => {
      const activityTime = (dm: DMItem) => {
        const channelMs = Date.parse(dm.updated_at);
        const bubbleUpdatedAt = dmAgentBubbleActivity(dm, bubbleActivityByAgent)
          ?.latestUpdatedAt;
        if (!bubbleUpdatedAt) return Number.isNaN(channelMs) ? 0 : channelMs;
        const bubbleMs = Date.parse(bubbleUpdatedAt);
        if (Number.isNaN(bubbleMs)) return Number.isNaN(channelMs) ? 0 : channelMs;
        return Math.max(Number.isNaN(channelMs) ? 0 : channelMs, bubbleMs);
      };
      const delta = activityTime(b) - activityTime(a);
      if (delta !== 0) return delta;
      return 0;
    });
  }, [searchQuery, unpinnedDms, bubbleActivityByAgent]);
  // LRM-764: supervised agent↔agent pairs fold into「智能体协作」, except pairs
  // that @-mention the viewer (`has_mention`) — those stay flat so a direct @
  // is never buried under a collapsed folder. Pins already left this list via
  // `unpinnedDms` and live in the unified PINNED section above.
  const directDms = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return filteredDms.filter((dm) => {
      if (dm.mode !== "agent_pair") return true;
      if (!dm.has_mention) return false;
      if (!q) return true;
      return (
        dm.participants?.some((participant) => participant.name.toLowerCase().includes(q)) ||
        dm.peer.name.toLowerCase().includes(q)
      );
    });
  }, [filteredDms, searchQuery]);

  const agentPairDms = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return filteredDms.filter((dm) => {
      if (dm.mode !== "agent_pair" || dm.has_mention) return false;
      if (!q) return true;
      return (
        dm.participants?.some((participant) => participant.name.toLowerCase().includes(q)) ||
        dm.peer.name.toLowerCase().includes(q)
      );
    });
  }, [filteredDms, searchQuery]);
  const agentPairUnread = useMemo(
    () =>
      sumUnmutedUnreadCounts(
        agentPairDms,
        (dm) => dm.real_unread ?? dm.unread ?? 0,
        (dm) => isConversationMuted(dm),
      ),
    [agentPairDms],
  );
  const hasSearchQuery = searchQuery.trim().length > 0;
  // Rows this region actually paints — the two buckets partition the filtered
  // list, so 0 means the body would otherwise be an empty fragment (LRM-1366).
  const visibleRowCount = directDms.length + agentPairDms.length;

  // Header "+" still available when the only DMs are pinned (they live above).
  // LRM-294: no Ask Wendy promo card — Wendy stays a normal DM row / picker entry.
  const showHeaderTrigger = dmsPending || dms.length > 0;
  const openPicker = () => setPickerOpen(true);
  const closePicker = () => setPickerOpen(false);

  const listBody =
    !collapsed &&
    (dmsPending ? (
      <DmListSkeleton />
    ) : dms.length === 0 ? (
      <div className="flex flex-col items-center gap-2 px-3 py-3">
        <p className="text-xs text-foreground">{t(($) => $.dm.empty)}</p>
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
    ) : visibleRowCount === 0 ? (
      // LRM-1366: DMs exist but none of them render *here* — every one is
      // pinned (they live in the unified PINNED section) or the search only
      // matched pinned rows. Both used to render an empty fragment, leaving the
      // region as「heading + `+`」and nothing else, which reads as a broken list.
      hasSearchQuery ? (
        <div className="space-y-1 px-3 py-4 text-xs text-muted-foreground">
          <p className="font-medium text-foreground">
            {t(($) => $.sidebar.no_conversation_matches)}
          </p>
          <p>{t(($) => $.sidebar.search_scope_hint)}</p>
        </div>
      ) : (
        <p
          data-testid="dm-list-all-pinned"
          className="px-3 py-3 text-xs text-muted-foreground"
        >
          {t(($) => $.dm.all_pinned)}
        </p>
      )
    ) : (
      <>
        {directDms.map((dm) => {
          const bubble = dmAgentBubbleActivity(dm, bubbleActivityByAgent);
          return (
        <DmConversationRow
          key={`${dm.source}:${dm.id}`}
          dm={dm}
          active={activeId === dm.id}
          currentUserName={currentUserName}
          resolveMentionPreview={resolveMentionPreview}
          members={members}
          agents={agents}
          bubbleUnreadCount={bubble?.unreadCount ?? 0}
          bubbleLatestUpdatedAt={bubble?.latestUpdatedAt ?? null}
          onSelect={() => onSelect(dm)}
          onTogglePin={() => dmActions.togglePin(dm)}
          onMarkUnread={() => dmActions.markUnread(dm)}
          onToggleMute={() => dmActions.toggleMute(dm)}
          onClose={() => dmActions.close(dm)}
        />
          );
        })}
        {agentPairDms.length > 0 && (
          <div className="mt-1">
            <button
              type="button"
              onClick={() => setAgentDmsCollapsed((value) => !value)}
              className="flex w-full items-center gap-1 px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
              aria-expanded={!agentDmsCollapsed}
            >
              {agentDmsCollapsed ? (
                <ChevronRight className="size-3.5 shrink-0" />
              ) : (
                <ChevronDown className="size-3.5 shrink-0" />
              )}
              <span className="flex-1 text-left">
                {t(($) => $.dm.agent_pair.folder)}
              </span>
              <span className="text-[11px] tabular-nums">
                {agentPairDms.length}
              </span>
              {agentPairUnread > 0 && (
                <span className={CONVERSATION_SIDEBAR_UNREAD_BADGE}>
                  {agentPairUnread > 99 ? "99+" : agentPairUnread}
                </span>
              )}
            </button>
            {!agentDmsCollapsed &&
              agentPairDms.map((dm) => (
                <DmConversationRow
                  key={`${dm.source}:${dm.id}`}
                  dm={dm}
                  active={activeId === dm.id}
                  currentUserName={currentUserName}
                  resolveMentionPreview={resolveMentionPreview}
                  members={members}
                  agents={agents}
                  onSelect={() => onSelect(dm)}
                  onTogglePin={() => dmActions.togglePin(dm)}
                  onMarkUnread={() => dmActions.markUnread(dm)}
                  onToggleMute={() => dmActions.toggleMute(dm)}
                  onClose={() => dmActions.close(dm)}
                />
              ))}
          </div>
        )}
      </>
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
              <span className={CONVERSATION_SIDEBAR_UNREAD_BADGE}>
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
  const isMobile = useIsMobile();
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);
  const { openDM, isPending } = useOpenDM();
  const { getMemberHonor } = useActorName();
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
    // openDM swallows errors and returns null after toasting — keep the
    // picker open so the user can retry without re-opening the overlay.
    const dm = await openDM({ peer_type: item.kind, peer_id: item.id });
    if (dm) onClose();
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
          // Desktop popover can autofocus; mobile drawer + virtual keyboard
          // fight focus traps when the input steals focus on open.
          autoFocus={!isMobile}
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
                showStatusDot
                profileLink={false}
              />
              <ActorIdentityRow
                displayName={item.presentation.displayName}
                handle={item.presentation.handle}
                showHandle={item.presentation.showHandleLabel}
                honor={item.kind === "user" ? getMemberHonor(item.id) : undefined}
                showBadges={false}
                primaryClassName="truncate text-sm font-medium text-foreground"
              />
              <span className="shrink-0 text-[11px] text-muted-foreground">
                {item.kind === "agent"
                  ? t(($) => $.dm.agent_meta)
                  : t(($) => $.dm.human_meta)}
              </span>
            </button>
          ))
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// DM list row (also used by the unified PINNED section)
// ---------------------------------------------------------------------------

export function DmConversationRow({
  dm,
  active,
  currentUserName,
  resolveMentionPreview,
  members,
  agents,
  bubbleUnreadCount = 0,
  bubbleLatestUpdatedAt = null,
  onSelect,
  onTogglePin,
  onMarkUnread,
  onToggleMute,
  onClose,
}: {
  dm: DMItem;
  active: boolean;
  currentUserName: string | null;
  resolveMentionPreview: MentionPreviewResolver;
  members: MemberWithUser[];
  agents: Agent[];
  /** Unread independent bubble (chat_session) replies for this agent peer. */
  bubbleUnreadCount?: number;
  /** Newest bubble session `updated_at` — drives list time when bubble is unread. */
  bubbleLatestUpdatedAt?: string | null;
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
  const { getMemberHonor } = useActorName();
  const last = dm.last_message;
  // System rows (issue/member/project/reminder events) carry their own
  // localized narrative from the same structured facts the full in-channel
  // row renders (#634) — try that first so the preview never disagrees with
  // the message by falling back to the BE's raw English fallback `content`.
  const systemPreview = last ? formatSystemEventPreviewText(last, t, resolveMentionPreview) : null;
  const channelPreview =
    systemPreview ??
    (last
      ? formatChannelMessagePreview(
          resolveChannelAuthorDisplayName(last, { members, agents }),
          last.content,
          resolveMentionPreview,
          last.parts,
          {
            formatVoice: (seconds) =>
              seconds === null
                ? t(($) => $.message.voice_preview)
                : t(($) => $.message.voice_preview_duration, { seconds }),
          },
        )
      : "");
  const hasBubbleReply = bubbleUnreadCount > 0;
  const preview = hasBubbleReply
    ? t(($) => $.dm.bubble_replied_preview)
    : channelPreview;
  // Surface mentions of the viewer at full foreground weight (no bold) so an
  // @-mention reads as more salient than ordinary preview text.
  const mentionsUser =
    !!last &&
    !!currentUserName &&
    last.content.toLowerCase().includes(`@${currentUserName.toLowerCase()}`);
  const realUnread = (dm.real_unread ?? dm.unread ?? 0) + bubbleUnreadCount;
  const isManualDot = !!dm.manually_unread && realUnread === 0;
  const pinned = !!dm.pinned_at;
  const isMuted = isConversationMuted(dm);
  // peer.type "user" maps to the member-style avatar; agents get the presence
  // status dot. Both resolve name/avatar from the workspace queries.
  const actorType = dm.peer.type === "agent" ? "agent" : "member";
  // #692 supervised agent↔agent DM: the owner reads it read-only, so the row
  // shows BOTH agents (dual avatar + "A · B" title) and the 「智能体私聊」pill,
  // never a single human-looking peer. `participants` carries the two agents;
  // fall back to the direct `peer` rendering if the BE omitted it.
  const [pairA, pairB] = dm.participants ?? [];
  const agentPair = dm.mode === "agent_pair" && pairA && pairB ? { a: pairA, b: pairB } : null;
  const title = agentPair ? `${agentPair.a.name} · ${agentPair.b.name}` : dm.peer.name;
  // LRM-749/LRM-710: only a display-name collision earns a weak gray @handle
  // next to the peer name — unique names get zero extra pixels.
  const dupHandleLabel = useMemo(() => {
    if (dm.mode === "agent_pair" || dm.peer.type !== "agent") return null;
    return (
      computeDuplicatedHandleLabels(agents.filter((a) => !a.archived_at)).get(dm.peer.id) ?? null
    );
  }, [agents, dm.mode, dm.peer.id, dm.peer.type]);
  const peerHonor =
    !agentPair && dm.peer.type === "user" ? getMemberHonor(dm.peer.id) : undefined;
  const titleWeightClass = realUnread > 0 ? "font-semibold" : "font-medium";

  return (
    <ContextMenu>
      <ContextMenuTrigger
        render={
          <div
            data-pinned={pinned ? "true" : undefined}
            className={cn(
              "group/row relative mb-0.5 rounded-lg transition-colors",
              active ? CONVERSATION_SIDEBAR_ROW_ACTIVE : CONVERSATION_SIDEBAR_ROW_IDLE,
            )}
          />
        }
      >
        <button
          type="button"
          onClick={onSelect}
          className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left"
        >
          {agentPair ? (
            <div className="relative size-10 shrink-0" aria-hidden={false}>
              <ActorAvatar
                actorType="agent"
                actorId={agentPair.a.id}
                size={28}
                showStatusDot={false}
                profileLink={false}
              />
              <div className="absolute -bottom-0.5 -right-0.5 rounded-full ring-2 ring-background">
                <ActorAvatar
                  actorType="agent"
                  actorId={agentPair.b.id}
                  size={24}
                  showStatusDot={false}
                  profileLink={false}
                />
              </div>
            </div>
          ) : (
            // Soft pad + overflow-visible: presence ring/dot can paint past the
            // avatar square without layout shift (LRM-1119).
            <div className="shrink-0 overflow-visible p-0.5 -m-0.5">
              <ActorAvatar
                actorType={actorType}
                actorId={dm.peer.id}
                size={40}
                showStatusDot
                profileLink={false}
              />
            </div>
          )}
          <div className="min-w-0 flex-1">
            <div className="flex items-center justify-between gap-2">
              <span className="flex min-w-0 items-center gap-1">
                {pinned && (
                  <Pin className="size-3 shrink-0 -rotate-45 fill-muted-foreground/70 text-muted-foreground/70" />
                )}
                {agentPair ? (
                  <span
                    className={cn(
                      "truncate text-sm text-foreground",
                      titleWeightClass,
                    )}
                  >
                    {title}
                  </span>
                ) : (
                  <>
                    <ActorStyledName
                      displayName={title}
                      honor={peerHonor}
                      showBadges={false}
                      className={cn("text-sm text-foreground", titleWeightClass)}
                    />
                    {dupHandleLabel && (
                      <span className="shrink-0 text-[11px] font-normal text-muted-foreground">
                        {dupHandleLabel}
                      </span>
                    )}
                  </>
                )}
                {agentPair && (
                  <span className="shrink-0 rounded border border-border px-1 text-[10px] font-medium leading-tight text-muted-foreground">
                    {t(($) => $.dm.agent_pair.pill)}
                  </span>
                )}
                {isMuted && <MutedIndicator label={t(($) => $.dm.muted_label)} />}
              </span>
              {(last || hasBubbleReply) && (
                <span className="shrink-0 text-[11px] text-muted-foreground">
                  {/* LRM-762/763: even bubble-bumped rows use `<Time kind="list">`
                      — never the hard-coded 「刚刚」/just now copy. Prefer the
                      bubble session clock when that is what bumped the row. */}
                  {hasBubbleReply && bubbleLatestUpdatedAt ? (
                    <Time kind="list" value={bubbleLatestUpdatedAt} />
                  ) : last ? (
                    <Time kind="list" value={last.created_at} />
                  ) : null}
                </span>
              )}
            </div>
            <div className="mt-0.5 flex items-center justify-between gap-2">
              <span
                className={cn(
                  "truncate text-xs",
                  hasBubbleReply || mentionsUser
                    ? "text-foreground"
                    : "text-muted-foreground",
                  hasBubbleReply && "font-medium",
                )}
              >
                {preview}
              </span>
              {/* No @-mention dot in DMs by design (Iris/#303): every DM
                  message is already directed at you, so the dot carries no
                  extra signal over the unread count — it earns its place in
                  channels, to separate an @-mention from ambient chatter. */}
              <ConversationUnreadAffordance
                realUnread={realUnread}
                isManualDot={isManualDot}
                isMuted={isMuted}
                unreadLabel={t(($) => $.sidebar.unread_indicator, {
                  count: realUnread,
                })}
              />
            </div>
          </div>
        </button>
        {/* LRM-752/LRM-722: pin/unread/mute/close are viewer-side list
            preferences, not message mutations — agent_pair rows get the same
            ⋯ menu and right-click menu as regular DMs (BE 放行后不再 403). */}
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

/**
 * Pin/mute/unread/close handlers for a DM row — shared by DmList and the
 * unified PINNED section so both surfaces stay in lockstep.
 */
export function useDmRowActions() {
  const { t } = useT("channels");
  const setPinned = useSetDMPinned();
  const markUnread = useMarkDMUnread();
  const closeDM = useCloseDM();
  const muteDM = useMuteDM();
  const onError = () => showErrorToast(t(($) => $.dm.action_failed));

  return {
    togglePin: (dm: DMItem) =>
      setPinned.mutate({ source: dm.source, id: dm.id, pinned: !dm.pinned_at }, { onError }),
    markUnread: (dm: DMItem) =>
      markUnread.mutate({ source: dm.source, id: dm.id }, { onError }),
    toggleMute: (dm: DMItem) =>
      muteDM.mutate(
        { source: dm.source, id: dm.id, muted: !isConversationMuted(dm) },
        { onError },
      ),
    close: (dm: DMItem) =>
      closeDM.mutate({ source: dm.source, id: dm.id }, { onError }),
  };
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
