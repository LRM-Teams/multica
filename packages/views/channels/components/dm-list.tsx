"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Loader2, Mail, Pin, PinOff, Plus, Search, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  dmListOptions,
  useSetDMPinned,
  useMarkDMUnread,
  useCloseDM,
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
import { ActorAvatar } from "../../common/actor-avatar";
import { useT, useTimeAgo } from "../../i18n";
import { useOpenDM } from "../../common/use-open-dm";

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
  onSelect,
}: {
  /** Currently open conversation id (DM or group) — drives row highlight. */
  activeId: string | null;
  /** Viewer's display name, used to detect mentions in the last-message preview. */
  currentUserName: string | null;
  onSelect: (dm: DMItem) => void;
}) {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const wsId = useWorkspaceId();
  const { data: dms = [], isLoading } = useQuery(dmListOptions(wsId));
  const [collapsed, setCollapsed] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);

  const setPinned = useSetDMPinned();
  const markUnread = useMarkDMUnread();
  const closeDM = useCloseDM();

  const onError = () => toast.error(t(($) => $.dm.action_failed));
  const handleTogglePin = (dm: DMItem) =>
    setPinned.mutate({ source: dm.source, id: dm.id, pinned: !dm.pinned_at }, { onError });
  const handleMarkUnread = (dm: DMItem) =>
    markUnread.mutate({ source: dm.source, id: dm.id }, { onError });
  const handleClose = (dm: DMItem) =>
    closeDM.mutate({ source: dm.source, id: dm.id }, { onError });

  const aggregateUnread = useMemo(
    () => dms.reduce((sum, dm) => sum + (dm.unread ?? 0), 0),
    [dms],
  );

  // Pinned conversations float to the top of DIRECT MESSAGES; the server keeps
  // each group recency-sorted, so a stable sort preserves that order within the
  // pinned and unpinned groups.
  const sortedDms = useMemo(
    () => [...dms].sort((a, b) => (b.pinned_at ? 1 : 0) - (a.pinned_at ? 1 : 0)),
    [dms],
  );

  return (
    <div className="pb-1">
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

        {/* Hide the compact "+" when the list is empty — the empty-state CTA
            below is the sole entry point there, avoiding two visible triggers. */}
        {(isLoading || dms.length > 0) && (
          <NewDmPicker open={pickerOpen} onOpenChange={setPickerOpen} />
        )}
      </div>

      {!collapsed &&
        (isLoading ? (
          <div className="space-y-2 p-2">
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
          </div>
        ) : dms.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-3 py-3">
            <p className="text-xs text-muted-foreground">{t(($) => $.dm.empty)}</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="w-full text-xs"
              onClick={() => setPickerOpen(true)}
            >
              <Plus className="size-3.5" />
              {t(($) => $.dm.empty_cta)}
            </Button>
          </div>
        ) : (
          sortedDms.map((dm) => (
            <DmRow
              key={`${dm.source}:${dm.id}`}
              dm={dm}
              active={activeId === dm.id}
              currentUserName={currentUserName}
              timeAgo={timeAgo}
              onSelect={() => onSelect(dm)}
              onTogglePin={() => handleTogglePin(dm)}
              onMarkUnread={() => handleMarkUnread(dm)}
              onClose={() => handleClose(dm)}
            />
          ))
        ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// New DM picker — Popover (desktop) / Drawer (mobile)
// ---------------------------------------------------------------------------

function NewDmPicker({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const pickerBody = <DmPickerContent onClose={() => onOpenChange(false)} />;
  const triggerCls =
    "flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground";

  if (isMobile) {
    return (
      <>
        <button
          type="button"
          aria-label={t(($) => $.dm.new_aria)}
          onClick={() => onOpenChange(true)}
          className={triggerCls}
        >
          <Plus className="size-4" />
        </button>
        <Drawer open={open} onOpenChange={onOpenChange}>
          <DrawerContent className="px-4 pb-8">
            <DrawerHeader className="px-0 pb-3">
              <DrawerTitle>{t(($) => $.dm.new_title)}</DrawerTitle>
            </DrawerHeader>
            {pickerBody}
          </DrawerContent>
        </Drawer>
      </>
    );
  }

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger
        render={<button type="button" aria-label={t(($) => $.dm.new_aria)} className={triggerCls} />}
      >
        <Plus className="size-4" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <div className="border-b px-3 py-2.5">
          <p className="text-sm font-medium">{t(($) => $.dm.new_title)}</p>
        </div>
        {pickerBody}
      </PopoverContent>
    </Popover>
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
    const q = search.trim().toLowerCase();
    const agentItems = agents
      .filter((a) => !a.archived_at)
      .map((a) => ({ kind: "agent" as const, id: a.id, name: a.name }));
    const memberItems = members
      .filter((m) => m.user_id !== currentUser?.id)
      .map((m) => ({ kind: "user" as const, id: m.user_id, name: m.name }));
    const all = [...agentItems, ...memberItems];
    if (!q) return all;
    return all.filter((item) => item.name.toLowerCase().includes(q));
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
              <span className="min-w-0 flex-1 truncate text-sm">{item.name}</span>
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
  onSelect,
  onTogglePin,
  onMarkUnread,
  onClose,
}: {
  dm: DMItem;
  active: boolean;
  currentUserName: string | null;
  timeAgo: (dateStr: string) => string;
  onSelect: () => void;
  /** Pin / unpin (toggles based on current pinned state). */
  onTogglePin: () => void;
  /** Mark the conversation manually unread. */
  onMarkUnread: () => void;
  /** Close Chat — soft-hide the conversation (recoverable). */
  onClose: () => void;
}) {
  const { t } = useT("channels");
  const last = dm.last_message;
  const preview = last ? `${last.author_name}: ${last.content}`.replace(/\s+/g, " ") : "";
  // Surface mentions of the viewer at full foreground weight (no bold) so an
  // @-mention reads as more salient than ordinary preview text.
  const mentionsUser =
    !!last &&
    !!currentUserName &&
    last.content.toLowerCase().includes(`@${currentUserName.toLowerCase()}`);
  const unread = dm.unread ?? 0;
  const pinned = !!dm.pinned_at;
  // peer.type "user" maps to the member-style avatar; agents get the presence
  // status dot. Both resolve name/avatar from the workspace queries.
  const actorType = dm.peer.type === "agent" ? "agent" : "member";

  return (
    <ContextMenu>
      <ContextMenuTrigger
        render={
          <button
            type="button"
            onClick={onSelect}
            className={cn(
              "mb-0.5 flex w-full items-center gap-2.5 rounded-lg px-2 py-2 text-left transition-colors",
              active ? "bg-primary/[0.08]" : "hover:bg-accent",
            )}
          />
        }
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
            {unread > 0 && (
              <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
                {unread > 99 ? "99+" : unread}
              </span>
            )}
          </div>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onClick={onMarkUnread}>
          <Mail />
          {t(($) => $.dm.mark_unread)}
        </ContextMenuItem>
        <ContextMenuItem onClick={onTogglePin}>
          {pinned ? <PinOff /> : <Pin />}
          {pinned ? t(($) => $.dm.unpin) : t(($) => $.dm.pin)}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onClick={onClose}>
          <X />
          {t(($) => $.dm.close_chat)}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
