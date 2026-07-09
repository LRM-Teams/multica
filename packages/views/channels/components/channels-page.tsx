"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Archive,
  ArchiveRestore,
  ArrowLeft,
  Bell,
  BellOff,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  FileText,
  Hash,
  Mail,
  MessageCircle,
  MessageSquare,
  MoreHorizontal,
  Paperclip,
  PieChart,
  Pin,
  PinOff,
  Plus,
  Search,
  Share2,
  Smartphone,
  Square,
  Users,
  X,
} from "lucide-react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  activeChannelTasksKeys,
  activeChannelTasksOptions,
  channelMessageThreadOptions,
  channelKeys,
  channelMessagesPageOptions,
  channelMessagesFirstItemIndex,
  flattenChannelMessagePages,
  useEnsureMessageLoaded,
  channelsOptions,
  archivedChannelsOptions,
  channelMembersOptions,
  channelProjectOptions,
  useSetChannelProject,
  useAddChannelMembers,
  useCreateChannel,
  useDeleteChannel,
  useArchiveChannel,
  useRestoreChannel,
  useSetChannelPin,
  useMarkChannelRead,
  useMarkChannelUnread,
  useMuteChannel,
  useRemoveChannelMember,
  useSendChannelMessage,
  useSendChannelThreadMessage,
  useEditChannelMessage,
  useDeleteChannelMessage,
  useAddChannelReaction,
  useRemoveChannelReaction,
  useMarkChannelThreadRead,
  useSetChannelTyping,
  useComposerDraftStore,
  type ComposerDraftKey,
} from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { dmKeys, dmListOptions, useCreateOrFindDM } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { api } from "@multica/core/api";
import { useFileUpload, type UploadResult } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWSEvent } from "@multica/core/realtime";
import { toast } from "sonner";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import {
  matchesActorIdentitySearch,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type {
  Channel,
  ChannelActiveTask,
  ChannelMember,
  ChannelMemberBrief,
  ChannelMessage,
  ChannelMessageSearchResult,
  ChannelTypingPayload,
} from "@multica/core/types";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { UnicodeSpinner } from "@multica/ui/components/common/unicode-spinner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
} from "@multica/ui/components/ui/drawer";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  ResizablePanelGroup,
  ResizablePanel,
  ResizableHandle,
} from "@multica/ui/components/ui/resizable";
import { useDefaultLayout } from "react-resizable-panels";
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
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { cn } from "@multica/ui/lib/utils";
import { SidebarTrigger, useSidebarSafe } from "@multica/ui/components/ui/sidebar";
import { MobileListDetailLayout } from "../../common/mobile-list-detail-layout";
import { ContentEditor, type ContentEditorRef } from "../../editor/content-editor";
import { useNavigation } from "../../navigation/context";
import { agentColor } from "../../common/agent-color";
import { ProjectPickerButton } from "../../common/project-picker-button";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n/use-t";
import { useTimeAgo } from "../../i18n/use-time-ago";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { composePayloadKey } from "../hooks/use-compose-send-intent";
import { useComposerSend } from "../hooks/use-composer-send";
import { useEntryReadCursor } from "../hooks/use-entry-read-cursor";
import { useEntryAnchor } from "../hooks/use-entry-around-seq";
import { isChannelNameTakenError } from "../channel-create-error";
import { ChannelMessageList } from "./channel-message-list";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ChannelStatsPanel } from "./channel-stats-panel";
import { ChannelGroupAvatar } from "./channel-group-avatar";
import { ThreadPanel } from "./thread-panel";
import { ComposerQuotePreview } from "./message-quote";
import { mapThreadWakeAnnotations } from "./thread-read-model";
import {
  Composer,
  ConversationHeader,
  ReadOnlyConversationBanner,
} from "./conversation-surface";
import { DmConversationRow, DmList, useDmRowActions } from "./dm-list";
import { DmConversation } from "./dm-conversation";
import {
  formatChannelMessagePreview,
  resolveChannelAuthorDisplayName,
  type MentionPreviewResolver,
} from "./message-preview";
import {
  ConversationUnreadAffordance,
  isConversationMuted,
  MutedIndicator,
  sumUnmutedUnreadCounts,
} from "./conversation-muted";
import { buildPinnedConversationEntries } from "./pinned-conversations";
import { PinnedConversationsSection } from "./pinned-conversations-section";
import { AgentSidePanel } from "./agent-side-panel";

export interface TypingActor {
  key: string;
  channelId: string;
  actorName: string;
  actorType: ChannelTypingPayload["actor_type"];
  expiresAt: number;
}

const EMPTY_TYPING_ACTORS: TypingActor[] = [];
const EMPTY_ACTIVE_TASKS: ChannelActiveTask[] = [];
const identitySearchOptions = { extendedMatch: matchesPinyin };

// Overlapping avatar stack for the channel roster (agents tinted by identity
// color, humans as initials). The whole stack is the trigger that opens member
// management — matching the Figma header where the roster doubles as the
// add/remove entry point.
function MemberStack({
  members,
  max = 4,
  size = 28,
  emptyHint = true,
}: {
  members: ChannelMemberBrief[];
  max?: number;
  size?: number;
  emptyHint?: boolean;
}) {
  const { t } = useT("channels");
  if (members.length === 0) {
    return emptyHint ? (
      <span className="text-xs text-muted-foreground">{t(($) => $.members.empty)}</span>
    ) : null;
  }
  const visible = members.slice(0, max);
  const overflow = members.length - visible.length;
  const overlap = Math.round(size * 0.3);
  return (
    <span className="inline-flex items-center">
      {visible.map((m, i) => (
        <span
          key={`${m.member_type}:${m.member_id}`}
          style={{ marginLeft: i === 0 ? 0 : -overlap }}
          className="inline-flex rounded-full ring-2 ring-background"
        >
          {(() => {
            const name = resolveActorDisplayName(
              m,
              m.member_type === "agent" ? "Agent" : "Member",
            );
            return (
              <ActorAvatar
                name={name}
                initials={initialsOf(name || "?")}
                isAgent={m.member_type === "agent"}
                size={size}
                tint={m.member_type === "agent" ? agentColor(m.member_id) : undefined}
              />
            );
          })()}
        </span>
      ))}
      {overflow > 0 && (
        <span
          style={{ marginLeft: -overlap, width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.36)) }}
          className="inline-flex items-center justify-center rounded-full bg-muted font-medium text-muted-foreground ring-2 ring-background"
        >
          +{overflow}
        </span>
      )}
    </span>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useT("channels");
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="max-w-md rounded-3xl border bg-card p-8 text-center shadow-sm">
        <div className="mx-auto flex size-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
          <MessageCircle className="size-6" />
        </div>
        <h2 className="mt-5 text-xl font-semibold">{t(($) => $.empty_state.title)}</h2>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          {t(($) => $.empty_state.description)}
        </p>
        <Button className="mt-5" onClick={onCreate}>
          <Plus className="size-4" /> {t(($) => $.empty_state.cta)}
        </Button>
      </div>
    </div>
  );
}

function ConversationSwitchSkeleton({ isMobile }: { isMobile: boolean }) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      <header
        className={cn(
          "flex items-center justify-between gap-3 border-b py-2.5",
          isMobile ? "px-2" : "px-5",
        )}
      >
        <div className="flex min-w-0 items-center gap-3">
          {isMobile && <Skeleton className="size-10 shrink-0 rounded-md" />}
          <Skeleton className="size-10 shrink-0 rounded-full" />
          <div className="min-w-0 space-y-2">
            <Skeleton className="h-4 w-36" />
            <Skeleton className="h-3 w-48 max-w-[50vw]" />
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Skeleton className="size-8 rounded-md" />
          {!isMobile && <Skeleton className="size-8 rounded-md" />}
        </div>
      </header>
      <div className="min-h-0 flex-1 space-y-4 overflow-hidden px-4 py-5">
        <div className="flex gap-3">
          <Skeleton className="size-8 shrink-0 rounded-full" />
          <div className="min-w-0 flex-1 space-y-2">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-16 w-4/5 rounded-lg" />
          </div>
        </div>
        <div className="flex justify-end gap-3">
          <div className="min-w-0 flex-1 space-y-2">
            <Skeleton className="ml-auto h-3 w-20" />
            <Skeleton className="ml-auto h-14 w-3/5 rounded-lg" />
          </div>
        </div>
        <div className="flex gap-3">
          <Skeleton className="size-8 shrink-0 rounded-full" />
          <div className="min-w-0 flex-1 space-y-2">
            <Skeleton className="h-3 w-28" />
            <Skeleton className="h-20 w-2/3 rounded-lg" />
          </div>
        </div>
      </div>
      <div className="px-4 pb-4">
        <div className="rounded-xl border bg-card p-3 shadow-sm">
          <Skeleton className="h-12 w-full rounded-md" />
          <div className="mt-2 flex items-center justify-between">
            <div className="flex gap-2">
              <Skeleton className="size-8 rounded-md" />
              <Skeleton className="h-8 w-24 rounded-md" />
            </div>
            <Skeleton className="h-8 w-20 rounded-md" />
          </div>
        </div>
      </div>
    </div>
  );
}

function MobileSidebarTrigger() {
  const sidebar = useSidebarSafe();
  if (!sidebar) return null;
  return <SidebarTrigger className="mr-2 md:hidden" />;
}

function InitialChannelsShellSkeleton() {
  return (
    <div className="flex h-full min-h-0 bg-background">
      <aside className="hidden w-72 shrink-0 flex-col border-r bg-muted/20 md:flex">
        <div className="space-y-3 p-4">
          <Skeleton className="h-6 w-28" />
          <Skeleton className="h-9 w-full rounded-md" />
          <Skeleton className="h-12 w-full rounded-lg" />
          <Skeleton className="h-12 w-full rounded-lg" />
          <Skeleton className="h-12 w-full rounded-lg" />
        </div>
      </aside>
      <ConversationSwitchSkeleton isMobile={false} />
    </div>
  );
}

export function ConversationActivityStrip({
  typingActors = EMPTY_TYPING_ACTORS,
  tasks = EMPTY_ACTIVE_TASKS,
  stoppingTaskId = null,
  onStopTask,
}: {
  typingActors?: TypingActor[];
  tasks?: ChannelActiveTask[];
  stoppingTaskId?: string | null;
  onStopTask?: (task: ChannelActiveTask) => void;
}) {
  const { t } = useT("channels");
  const typingNames = useMemo(
    () => typingActors.flatMap((a) => {
      const name = a.actorName.trim();
      return name ? [name] : [];
    }),
    [typingActors],
  );
  const agentNames = useMemo(() => {
    const seen = new Set<string>();
    const unique: string[] = [];
    for (const task of tasks) {
      const name = task.agent_name.trim();
      if (!name || seen.has(name)) continue;
      seen.add(name);
      unique.push(name);
    }
    return unique;
  }, [tasks]);
  const typingLabel =
    typingNames.length === 0
      ? null
      : typingNames.length === 1
        ? t(($) => $.typing.single, { name: typingNames[0]! })
        : typingNames.length === 2
          ? t(($) => $.typing.pair, { a: typingNames[0]!, b: typingNames[1]! })
          : t(($) => $.typing.overflow, { a: typingNames[0]!, b: typingNames[1]!, count: typingNames.length });

  const agentLabel =
    agentNames.length === 0
      ? null
      : agentNames.length === 1
        ? t(($) => $.agent_status.processing_single, { name: agentNames[0]! })
        : agentNames.length === 2
          ? t(($) => $.agent_status.processing_pair, { a: agentNames[0]!, b: agentNames[1]! })
          : t(($) => $.agent_status.processing_overflow, {
              a: agentNames[0]!,
              b: agentNames[1]!,
              count: agentNames.length,
            });

  if (!typingLabel && !agentLabel) return null;

  return (
    <div
      className="flex min-h-6 items-center justify-between gap-2 px-5 pb-2 text-xs text-muted-foreground"
      aria-live="polite"
      data-testid="conversation-activity-strip"
    >
      <div className="flex min-w-0 items-center gap-1.5">
        {typingLabel ? (
          <span className="flex min-w-0 items-center gap-1 truncate">
            <span className="truncate">{typingLabel}</span>
            <TypingDots />
          </span>
        ) : null}
        {typingLabel && agentLabel ? (
          <span className="shrink-0 text-muted-foreground/50" aria-hidden="true">
            ·
          </span>
        ) : null}
        {agentLabel ? (
          <span className="flex min-w-0 items-center gap-1.5 truncate">
            <UnicodeSpinner className="shrink-0 text-muted-foreground/60" />
            <span className="truncate">{agentLabel}</span>
          </span>
        ) : null}
      </div>
      {onStopTask && tasks.length > 0 ? (
        <div className="flex shrink-0 items-center gap-1 overflow-x-auto">
          {tasks.map((task) => (
            <Button
              key={task.task_id}
              type="button"
              variant="ghost"
              size="sm"
              className="h-6 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
              disabled={stoppingTaskId === task.task_id}
              onClick={() => onStopTask(task)}
              aria-label={t(($) => $.agent_status.stop_aria, { name: task.agent_name })}
            >
              <Square className="size-2.5 fill-current" />
              {tasks.length === 1 ? t(($) => $.agent_status.stop) : t(($) => $.agent_status.stop_named, { name: task.agent_name })}
            </Button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function TypingDots() {
  return (
    <span className="flex shrink-0 items-end gap-0.5" aria-hidden="true">
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60 [animation-delay:-0.24s]" />
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60 [animation-delay:-0.12s]" />
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60" />
    </span>
  );
}

interface ChannelsPageProps {
  /** The selected channel or DM id, from the /channels/[id] route segment. */
  channelId?: string;
}

// ChannelsPage's many useState calls predate #309 — this routing change reduced
// the count, it did not add to it. Consolidating them into useReducer is a
// refactor of a ~2500-line component, out of scope for a URL-format change and
// tracked separately; suppress the pre-existing warning rather than block on it.
// react-doctor-disable-next-line react-doctor/prefer-useReducer
export function ChannelsPage({ channelId }: ChannelsPageProps = {}) {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const { searchParams, replace, getShareableUrl } = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);
  const { mutate: markChannelRead } = useMarkChannelRead();
  const isMobile = useIsMobile();
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_channels_layout",
  });
  // Mobile-only: the header's right-side actions collapse into a single "⋯"
  // button that opens a bottom Drawer (vaul, with drag handle). `"menu"` shows
  // the action list (Members / Share / Stats / Files); picking one swaps the
  // Drawer body to that section. A header Popover can render off-screen on a
  // narrow viewport, so the drawer is the reliable container. `null` = closed.
  const [mobilePanel, setMobilePanel] = useState<
    "menu" | "members" | "stats" | "files" | null
  >(null);
  // Selected channel. Resolved from the `channelId` route param below, since we
  // don't yet know (until channels/dms load) whether it's a channel or a DM.
  const [activeId, setActiveId] = useState<string | null>(null);
  // Selected DM (Direct Messages region). Mutually exclusive with the group
  // selection: opening a DM clears `activeId`, opening a group clears this.
  const [activeDmId, setActiveDmId] = useState<string | null>(null);
  // The last `/channels/[id]` route id we reconciled into the selection above.
  // A ref (not state): it's bookkeeping that gates the reconciliation below, not
  // a render input. Tracking it lets the reconciliation fire only on a genuine
  // ROUTE change, never on an optimistic in-page selection that momentarily runs
  // ahead of the async route commit. `undefined` = not yet reconciled.
  const reconciledRouteIdRef = useRef<string | undefined>(undefined);
  // ?message= deep-links to a specific message (e.g. from an overview mention).
  // We scroll to and briefly highlight it, then clear so it fades out.
  const [highlightMessageId, setHighlightMessageId] = useState<string | null>(
    () => searchParams.get("message"),
  );
  const [search, setSearch] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [channelsCollapsed, setChannelsCollapsed] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null);
  const [archiveTarget, setArchiveTarget] = useState<Channel | null>(null);
  const [archivedOpen, setArchivedOpen] = useState(false);
  const editorRef = useRef<ContentEditorRef>(null);
  const composerDrafts = useComposerDraftStore((s) => s.drafts);
  const storeSetComposerDraft = useComposerDraftStore((s) => s.setDraft);
  const storeClearComposerDraft = useComposerDraftStore((s) => s.clearDraft);
  const [typingActors, setTypingActors] = useState<Record<string, TypingActor>>({});
  const [newName, setNewName] = useState("");
  const [newLarkChatId, setNewLarkChatId] = useState("");
  // Inline "name required" hint for the create popover. Empty names used to
  // silently default to "general", which collided with an existing general
  // channel and surfaced as an opaque failure (#216).
  const [createNameError, setCreateNameError] = useState(false);
  // Multi-select invite: keys are `${type}:${id}` so users and agents share one set.
  const [selectedInvites, setSelectedInvites] = useState<Set<string>>(new Set());
  const [memberTab, setMemberTab] = useState<"invite" | "members">("invite");
  const [memberQuery, setMemberQuery] = useState("");
  const typingStartedRef = useRef(false);
  const typingStopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const typingPulseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Send idempotency + send lock (#207). Channel top-level and thread reply each
  // own an independent intent so an in-flight channel send never blocks a thread
  // reply, and vice versa.
  const channelSend = useComposerSend();
  const threadSend = useComposerSend();
  const threadEditorRef = useRef<ContentEditorRef>(null);
  const focusThreadComposerOnOpenRef = useRef(false);
  const [sidePanelState, setSidePanelState] = useState<{
    openThreadRoot: ChannelMessage | null;
    selectedAgentPanelId: string | null;
    threadDraftEmpty: boolean;
    channelQuote: ChannelMessage | null;
    threadQuote: ChannelMessage | null;
  }>({
    openThreadRoot: null,
    selectedAgentPanelId: null,
    threadDraftEmpty: true,
    channelQuote: null,
    threadQuote: null,
  });
  const { openThreadRoot, selectedAgentPanelId, threadDraftEmpty, channelQuote, threadQuote } = sidePanelState;
  const setOpenThreadRoot = useCallback((next: ChannelMessage | null) => {
    setSidePanelState((current) => ({ ...current, openThreadRoot: next }));
  }, []);
  const setSelectedAgentPanelId = useCallback((next: string | null) => {
    setSidePanelState((current) => ({ ...current, selectedAgentPanelId: next }));
  }, []);
  const setThreadDraftEmpty = useCallback((next: boolean) => {
    setSidePanelState((current) => ({ ...current, threadDraftEmpty: next }));
  }, []);
  const setChannelQuote = useCallback((next: ChannelMessage | null) => {
    setSidePanelState((current) => ({ ...current, channelQuote: next }));
  }, []);
  const setThreadQuote = useCallback((next: ChannelMessage | null) => {
    setSidePanelState((current) => ({ ...current, threadQuote: next }));
  }, []);
  const resetSidePanelState = useCallback(() => {
    setSidePanelState({
      openThreadRoot: null,
      selectedAgentPanelId: null,
      threadDraftEmpty: true,
      channelQuote: null,
      threadQuote: null,
    });
  }, []);
  const [convSearchOpen, setConvSearchOpen] = useState(false);
  const [convSearchQuery, setConvSearchQuery] = useState("");
  const [convSearchResults, setConvSearchResults] = useState<ChannelMessageSearchResult[]>([]);
  const [convSearchTotal, setConvSearchTotal] = useState(0);
  const [convSearchIndex, setConvSearchIndex] = useState(0);
  const [viewportReady, setViewportReady] = useState(false);
  const previousMobileRef = useRef<boolean | null>(null);

  const { data: channels = [], isLoading } = useQuery(channelsOptions(wsId));
  const { data: archivedChannels = [] } = useQuery(archivedChannelsOptions(wsId));
  const { data: workspaceMembers = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const resolveMentionPreview = useMemo<MentionPreviewResolver>(
    () => (type, id, fallbackLabel) => {
      if (type === "agent") {
        const agent = agents.find((a) => a.id === id);
        return resolveActorDisplayName(agent, fallbackLabel);
      }
      const member = workspaceMembers.find((m) => m.user_id === id);
      return resolveActorDisplayName(member, fallbackLabel);
    },
    [agents, workspaceMembers],
  );
  // Desktop auto-selects the first channel so the stream pane is never blank.
  // Mobile is list-first: `active` resolves only from an explicit selection
  // (click or ?channel= deep link), so the list shows until the user opens a
  // channel and the Back button (which clears activeId) returns to it.
  const { data: dms = [] } = useQuery(dmListOptions(wsId));

  // Reconcile the `/channels/[id]` route param into the active selection.
  // Adjusting state during render (not in an effect) keeps the reconciliation
  // keyed on a genuine change of the ROUTE id: an in-page click updates the
  // selection state and calls `replace()`, but on web the route commits
  // asynchronously, so `channelId` briefly still points at the previous
  // conversation. Because we compare against `reconciledRouteId` (the last route
  // we adopted) rather than the current selection, that stale value never drags
  // the click back — while a real external navigation (shared link,
  // notification, Cmd+K) still opens its target. The id alone doesn't reveal
  // channel-vs-DM, so resolution waits on list membership: until the lists load
  // we leave the ref unadvanced so this retries as they arrive.
  if (channelId !== reconciledRouteIdRef.current) {
    if (!channelId) {
      setActiveId(null);
      setActiveDmId(null);
      reconciledRouteIdRef.current = undefined;
    } else if (channelId === activeId || channelId === activeDmId) {
      reconciledRouteIdRef.current = channelId;
    } else if (
      channels.some((c) => c.id === channelId) ||
      archivedChannels.some((c) => c.id === channelId)
    ) {
      setActiveId(channelId);
      setActiveDmId(null);
      reconciledRouteIdRef.current = channelId;
    } else if (dms.some((d) => d.id === channelId)) {
      setActiveId(null);
      setActiveDmId(channelId);
      reconciledRouteIdRef.current = channelId;
    }
    // else: lists still loading — leave the ref unadvanced to retry.
  }

  // Resolve the selected DM from the list. A DM selection takes priority over a
  // group selection (the two are mutually exclusive via the select handlers),
  // so when a DM is active we don't auto-resolve a group below.
  const activeDm = useMemo(
    () => (activeDmId ? dms.find((d) => d.id === activeDmId) ?? null : null),
    [dms, activeDmId],
  );
  // Desktop auto-selects the first channel only when nothing else is open —
  // never override an active DM selection. Mobile is list-first (no auto-open).
  const active = useMemo(() => {
    if (activeDmId) return null;
    const explicit =
      channels.find((c) => c.id === activeId) ??
      archivedChannels.find((c) => c.id === activeId) ??
      null;
    return isMobile ? explicit : (explicit ?? channels[0] ?? null);
  }, [channels, archivedChannels, activeId, activeDmId, isMobile]);
  const selectedAgentPanel = useMemo(
    () => (selectedAgentPanelId ? agents.find((agent) => agent.id === selectedAgentPanelId) ?? null : null),
    [agents, selectedAgentPanelId],
  );
  const isActiveArchived = !!active?.archived_at;
  const activeDraftKey = active ? (`channel:${active.id}` as const) : null;
  const activeDraft = activeDraftKey ? (composerDrafts[activeDraftKey]?.content ?? "") : "";
  const activeDraftEmpty = !activeDraft.trim();
  const setConversationDraft = useCallback((key: ComposerDraftKey, value: string) => {
    if (!value.trim()) {
      storeClearComposerDraft(key);
      return;
    }
    storeSetComposerDraft(key, value);
  }, [storeSetComposerDraft, storeClearComposerDraft]);
  // #340: freeze the entry read cursor + true unread count (sidebar-same source)
  // at entry — anchors the cold load on the unread divider and gives the divider
  // the real "N new" (not the count within the loaded window). See the hook.
  const entryAnchor = useEntryAnchor(
    active?.id,
    active?.last_read_seq,
    active?.real_unread_count ?? active?.unread_count,
  );
  const {
    data: messagePages,
    isLoading: messagesLoading,
    isError: messagesError,
    refetch: refetchMessages,
    fetchNextPage: fetchOlderMessages,
    hasNextPage: hasOlderMessages,
    isFetchingNextPage: isFetchingOlderMessages,
  } = useInfiniteQuery(
    channelMessagesPageOptions(active?.id ?? "", {
      aroundSeq: entryAnchor.aroundSeq,
    }),
  );
  const activeChannelId = active?.id ?? "";
  const messages = useMemo(() => flattenChannelMessagePages(activeChannelId ? messagePages : undefined), [activeChannelId, messagePages]);
  const messagesFirstItemIndex = useMemo(
    () => channelMessagesFirstItemIndex(activeChannelId ? messagePages : undefined, messages.length > 0),
    [activeChannelId, messagePages, messages.length],
  );
  const threadRoot = useMemo(
    () =>
      openThreadRoot && activeChannelId === openThreadRoot.channel_id
        ? messages.find((m) => m.id === openThreadRoot.id) ?? openThreadRoot
        : null,
    [activeChannelId, messages, openThreadRoot],
  );
  const { data: threadPage, isLoading: threadLoading, isError: threadError, refetch: refetchThread } = useQuery(
    channelMessageThreadOptions(activeChannelId, threadRoot?.id ?? ""),
  );
  const threadReplies = useMemo(
    () => {
      const messages = threadPage?.messages ?? [];
      return threadRoot ? messages.filter((msg) => msg.id !== threadRoot.id) : messages;
    },
    [threadPage?.messages, threadRoot],
  );
  // "Why no reply" wake strip (#196), agent-only + neutral, from the root's
  // read-model annotations (#251).
  const threadWakeAnnotations = useMemo(
    () => (threadRoot ? mapThreadWakeAnnotations(threadRoot) : []),
    [threadRoot],
  );
  const { data: channelMembers = [] } = useQuery(channelMembersOptions(active?.id ?? ""));
  const { data: channelProjectId = "" } = useQuery(channelProjectOptions(wsId, active?.id ?? ""));
  const { data: activeTasks = [] } = useQuery(activeChannelTasksOptions(active?.id ?? ""));
  const [stoppingChannelTaskId, setStoppingChannelTaskId] = useState<string | null>(null);
  const setChannelProject = useSetChannelProject(wsId, active?.id ?? "");
  const createChannel = useCreateChannel();
  const deleteChannel = useDeleteChannel();
  const archiveChannel = useArchiveChannel();
  const restoreChannel = useRestoreChannel();
  const setChannelPin = useSetChannelPin();
  const markChannelUnread = useMarkChannelUnread();
  const sendMessage = useSendChannelMessage();
  const sendThreadMessage = useSendChannelThreadMessage();
  const addChannelReaction = useAddChannelReaction();
  const removeChannelReaction = useRemoveChannelReaction();
  const editChannelMessage = useEditChannelMessage();
  const deleteChannelMessage = useDeleteChannelMessage();
  const { mutate: markThreadRead } = useMarkChannelThreadRead();
  const setTyping = useSetChannelTyping();
  // Edit is a PATCH of an existing message (H5) — it routes through
  // editChannelMessage, never the send path, so it can never produce a new wake.
  const handleEditMessage = useCallback((message: ChannelMessage, content: string) => {
    editChannelMessage.mutate(
      { channelId: message.channel_id, messageId: message.id, content },
      { onError: () => toast.error(t(($) => $.message.edit_failed_toast)) },
    );
  }, [editChannelMessage, t]);
  const handleDeleteMessage = useCallback((message: ChannelMessage) => {
    deleteChannelMessage.mutate(
      { channelId: message.channel_id, messageId: message.id },
      { onError: () => toast.error(t(($) => $.message.delete_failed_toast)) },
    );
  }, [deleteChannelMessage, t]);
  const handleReactToMessage = useCallback((message: ChannelMessage, emoji: string) => {
    const hasReacted = message.reactions?.some(
      (reaction) => reaction.actor_type === "member" && reaction.actor_id === currentUserId && reaction.emoji === emoji,
    );
    const vars = { channelId: message.channel_id, messageId: message.id, emoji };
    if (hasReacted) {
      removeChannelReaction.mutate(vars);
    } else {
      addChannelReaction.mutate(vars);
    }
  }, [addChannelReaction, currentUserId, removeChannelReaction]);
  const addMembers = useAddChannelMembers();
  const removeMember = useRemoveChannelMember();
  const createOrFindDm = useCreateOrFindDM();
  const { uploadWithToast } = useFileUpload(api);
  // Maps the URL the editor wrote into the markdown body → attachment row id,
  // so on send we bind only attachments still referenced in the content.
  // Mirrors the chat-input flow. Cleared after every successful send.
  const uploadMapRef = useRef<Map<string, string>>(new Map());
  const threadUploadMapRef = useRef<Map<string, string>>(new Map());
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);

  const memberIds = useMemo(
    () => new Set(channelMembers.filter((m) => m.member_type === "user").map((m) => m.member_id)),
    [channelMembers],
  );
  const agentIds = useMemo(
    () => new Set(channelMembers.filter((m) => m.member_type === "agent").map((m) => m.member_id)),
    [channelMembers],
  );
  const availableMembers = workspaceMembers.filter((m) => !memberIds.has(m.user_id));
  const availableAgents = agents.filter((a) => !agentIds.has(a.id) && !a.archived_at);
  // Flat, searchable candidate list (users + agents) for the invite tab.
  const inviteCandidates = useMemo(() => {
    const q = memberQuery.trim();
    const list: Array<{
      key: string;
      type: "user" | "agent";
      id: string;
      presentation: ActorIdentityPresentation;
    }> = [
      ...availableMembers.map((m) => ({
        key: `user:${m.user_id}`,
        type: "user" as const,
        id: m.user_id,
        presentation: resolveActorIdentityPresentation(m, m.email),
      })),
      ...availableAgents.map((a) => ({
        key: `agent:${a.id}`,
        type: "agent" as const,
        id: a.id,
        presentation: resolveActorIdentityPresentation(a, "Agent"),
      })),
    ];
    return q
      ? list.filter((c) =>
          matchesActorIdentitySearch(
            c.presentation.displayName,
            c.presentation.handle,
            q,
            identitySearchOptions,
          ),
        )
      : list;
  }, [availableMembers, availableAgents, memberQuery]);
  const filteredMembers = useMemo(() => {
    const q = memberQuery.trim();
    return q
      ? channelMembers.filter((m) =>
          matchesActorIdentitySearch(
            resolveActorDisplayName(
              m,
              m.member_type === "agent"
                ? t(($) => $.message.agent_badge)
                : t(($) => $.members.title),
            ),
            resolveActorHandle(m),
            q,
            identitySearchOptions,
          ),
        )
      : channelMembers;
  }, [channelMembers, memberQuery, t]);
  // Scope the composer's @ picker to this channel's members only.
  const channelMemberIds = useMemo(
    () => new Set(channelMembers.map((m) => m.member_id)),
    [channelMembers],
  );
  // Agents surface their lifecycle stage via the query-driven working indicator
  // (which shows "Thinking"/"Starting up" rather than a premature "typing"), so
  // filter agent actors out of the typing render to avoid showing them twice.
  // Human/member typing keeps using the transient broadcast.
  const activeTypingActors = useMemo(
    () =>
      Object.values(typingActors).filter(
        (a) => a.channelId === active?.id && a.actorType !== "agent",
      ),
    [active?.id, typingActors],
  );
  const rosterSummary = useMemo(
    () => {
      const memberCount = channelMembers.filter((m) => m.member_type === "user").length;
      const agentCount = channelMembers.filter((m) => m.member_type === "agent").length;
      if (memberCount === 0 && agentCount === 0) return "";
      return t(($) => $.header.roster_summary, { members: memberCount, agents: agentCount });
    },
    [channelMembers, t],
  );
  // Pinned conversations live in the unified PINNED section (Slack-style),
  // not floated to the top of Channels / Direct messages.
  const unpinnedChannels = useMemo(
    () => channels.filter((c) => !c.pinned_at),
    [channels],
  );
  const filteredChannels = useMemo(() => {
    const q = search.trim().toLowerCase();
    return q ? unpinnedChannels.filter((c) => c.name.toLowerCase().includes(q)) : unpinnedChannels;
  }, [unpinnedChannels, search]);
  const pinnedEntries = useMemo(
    () => buildPinnedConversationEntries(dms, channels),
    [dms, channels],
  );
  const filteredPinnedEntries = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return pinnedEntries;
    return pinnedEntries.filter((entry) => {
      if (entry.kind === "dm") return entry.dm.peer.name.toLowerCase().includes(q);
      return entry.channel.name.toLowerCase().includes(q);
    });
  }, [pinnedEntries, search]);
  const hasSidebarSearch = search.trim().length > 0;
  const dmActions = useDmRowActions();
  const currentUserRole = useMemo(
    () => workspaceMembers.find((m) => m.user_id === currentUserId)?.role ?? "member",
    [workspaceMembers, currentUserId],
  );
  const canArchive = useCallback(
    (channel: Channel) =>
      channel.created_by === currentUserId ||
      currentUserRole === "owner" ||
      currentUserRole === "admin",
    [currentUserId, currentUserRole],
  );
  // Collapsed CHANNELS badge covers unpinned only — pinned rows sit in PINNED.
  const aggregateChannelUnread = useMemo(
    () =>
      sumUnmutedUnreadCounts(
        unpinnedChannels,
        (c) => c.real_unread_count ?? c.unread_count ?? 0,
        (c) => isConversationMuted(c),
      ),
    [unpinnedChannels],
  );

  useEffect(() => {
    previousMobileRef.current =
      typeof window !== "undefined" ? window.innerWidth < 768 : false;
    setViewportReady(true);
  }, []);

  useEffect(() => {
    if (!viewportReady) return;
    const previous = previousMobileRef.current;
    if (previous === false && isMobile && !activeId && !activeDmId && channels[0]) {
      setActiveId(channels[0].id);
    }
    previousMobileRef.current = isMobile;
  }, [viewportReady, isMobile, activeId, activeDmId, channels]);

  useEffect(() => {
    // Mobile is list-first — don't auto-open a channel, or the list would never
    // be reachable. Desktop keeps auto-selecting the first channel.
    //
    // `useIsMobile()` reports `false` on the very first render (its internal
    // state is still `undefined`) even on a phone, so we can't trust it here on
    // mount. Measure the viewport directly — effects are client-only, so
    // `window` is always defined — to avoid auto-selecting (and thus forcing the
    // detail view) before the breakpoint is known.
    const onMobileViewport =
      isMobile || (typeof window !== "undefined" && window.innerWidth < 768);
    if (onMobileViewport) return;
    // Don't auto-open a group when a DM is the active selection.
    if (activeDmId) return;
    if (!activeId && channels[0]) setActiveId(channels[0].id);
  }, [activeId, activeDmId, channels, isMobile]);

  // Bottom-stick on new messages and open-at-latest on switch are handled by
  // ChannelMessageList (react-virtuoso followOutput + initialTopMostItemIndex).
  // The deep-linked message is scrolled into view inside the virtualized list
  // too; here we only clear the highlight after it has had time to flash.

  const searchHitIds = useMemo(
    () =>
      convSearchOpen && convSearchResults.length > 0
        ? new Set(convSearchResults.map((r) => r.message_id))
        : undefined,
    [convSearchOpen, convSearchResults],
  );
  const searchHighlightQuery = convSearchOpen ? convSearchQuery.trim() : "";
  const effectiveHighlightId = convSearchOpen
    ? (convSearchResults[convSearchIndex]?.message_id ?? null)
    : highlightMessageId;

  // A search hit or quote back-reference can point at a message that lives in
  // an older, not-yet-fetched page. The viewport can only scroll to a message
  // it has loaded, so drive the infinite query to page older history until the
  // target loads (found) or history is exhausted (not found).
  const jumpTargetLoaded = useMemo(
    () => !!effectiveHighlightId && messages.some((m) => m.id === effectiveHighlightId),
    [effectiveHighlightId, messages],
  );
  // `exhausted` (target not anywhere in history) is surfaced declaratively as
  // an inline notice below, so the jump never fails silently.
  const jumpStatus = useEnsureMessageLoaded({
    targetId: effectiveHighlightId,
    targetLoaded: jumpTargetLoaded,
    hasOlder: !!hasOlderMessages,
    isFetchingOlder: isFetchingOlderMessages,
    fetchOlder: fetchOlderMessages,
  });

  useEffect(() => {
    if (convSearchOpen) return;
    // Only flash-then-clear a quote highlight once its target is actually on
    // screen — while older pages are still being paged toward it, keep it set
    // so the scroll lands when it loads. The timer's cleanup keeps this a
    // reactive flash, not a state-driven event handler.
    if (!highlightMessageId || !jumpTargetLoaded) return;
    const clear = setTimeout(() => setHighlightMessageId(null), 2500);
    return () => clearTimeout(clear);
  }, [highlightMessageId, convSearchOpen, jumpTargetLoaded]);

  // Clear search and quote state when the active channel changes.
  // react-doctor-disable-next-line react-doctor/no-chain-state-updates -- route/list reconciliation owns channel switching; resetting dependent conversation state here matches existing lifecycle cleanup.
  useEffect(() => {
    setConvSearchOpen(false);
    setConvSearchQuery("");
    setConvSearchResults([]);
    setConvSearchTotal(0);
    setConvSearchIndex(0);
    // react-doctor-disable-next-line react-doctor/no-chain-state-updates -- quote state is dependent on the selected channel lifecycle.
    setChannelQuote(null);
  }, [active?.id, setChannelQuote]);

  // Debounced in-conversation search.
  useEffect(() => {
    if (!convSearchOpen || !active) return;
    const q = convSearchQuery.trim();
    if (!q) {
      setConvSearchResults([]);
      setConvSearchTotal(0);
      setConvSearchIndex(0);
      return;
    }
    const timer = setTimeout(async () => {
      try {
        const res = await api.searchChannelMessages(active.id, q);
        setConvSearchResults(res.results);
        setConvSearchTotal(res.total);
        setConvSearchIndex(0);
      } catch {
        toast.error(t(($) => $.conv_search.error));
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [convSearchQuery, active, convSearchOpen, t]);

  // Mark the conversation read when it becomes active (select / deep link /
  // auto-select) — clears the unread badge — and expose the pre-advance read
  // cursor from the mark-read response so the "N new messages" divider anchors
  // race-free (#303).
  const dividerLastReadSeq = useEntryReadCursor(
    active?.id,
    active?.last_read_seq,
    markChannelRead,
  );

  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      setTypingActors((current) => {
        const next = Object.fromEntries(Object.entries(current).filter(([, actor]) => actor.expiresAt > now));
        return Object.keys(next).length === Object.keys(current).length ? current : next;
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    typingStartedRef.current = false;
    if (typingStopTimerRef.current) window.clearTimeout(typingStopTimerRef.current);
    if (typingPulseTimerRef.current) window.clearTimeout(typingPulseTimerRef.current);
  }, [active?.id]);

  // react-doctor-disable-next-line react-doctor/no-chain-state-updates -- threadRoot is derived from paged data; the quote state must clear when that derived root disappears.
  useEffect(() => {
    if (!threadRoot) {
      // react-doctor-disable-next-line react-doctor/no-chain-state-updates -- quote state is dependent on the selected thread lifecycle.
      setThreadQuote(null);
      return;
    }
    if (!activeChannelId) return;
    markThreadRead({ channelId: activeChannelId, messageId: threadRoot.id });
  }, [activeChannelId, threadRoot, markThreadRead, setThreadQuote]);

  useEffect(() => {
    if (!threadRoot || !focusThreadComposerOnOpenRef.current) return;
    focusThreadComposerOnOpenRef.current = false;
    requestAnimationFrame(() => {
      threadEditorRef.current?.focus();
    });
  }, [threadRoot]);

  // New messages (from others / agents) refresh the list (unread + preview)
  // and the open thread. Keep the active channel marked read while viewing it.
  useWSEvent("channel:message", (payload) => {
    const e = payload as ChannelMessage;
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    // The DM list unions dm_channel items, so a channel message may change a DM
    // row's preview / unread — refresh it too.
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    if (e.channel_id) {
      qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(e.channel_id) });
      if (e.channel_id === active?.id) markChannelRead(active.id);
    }
  });

  // The DM list also unions legacy chat_sessions, so a chat message updates a
  // DM row even though it isn't a channel event.
  useWSEvent("chat:message", () => {
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("task:cancelled", (payload) => {
    const e = payload as { chat_session_id?: string };
    if (!e.chat_session_id || !active?.id) return;
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(active.id) });
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  // Another client deleted a channel — drop it from the list. If it was the
  // open one, `active` falls back to the first remaining channel via the memo.
  useWSEvent("channel:deleted", (payload) => {
    const e = payload as { id?: string };
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    if (e.id && e.id === activeId) setActiveId(null);
  });

  useWSEvent("channel:typing", (payload) => {
    const event = payload as ChannelTypingPayload;
    if (!event.channel_id || event.channel_id !== active?.id) return;
    // A typing pulse from an agent often coincides with a task starting or
    // ending — refresh the authoritative lifecycle view promptly.
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(event.channel_id) });
    const actorKey = `${event.actor_type}:${event.actor_id ?? event.actor_name}`;
    if (event.actor_type === "user" && event.actor_id && event.actor_id === currentUserId) return;
    setTypingActors((current) => {
      if (!event.is_typing) {
        const next = { ...current };
        delete next[actorKey];
        return next;
      }
      return {
        ...current,
        [actorKey]: {
          key: actorKey,
          channelId: event.channel_id,
          actorName: event.actor_name,
          actorType: event.actor_type,
          expiresAt: Date.now() + (event.expires_in_ms ?? 5000),
        },
      };
    });
  });

  const handleCreate = () => {
    // Require an explicit name — no silent "general" fallback (#216).
    const name = newName.trim();
    if (!name) {
      setCreateNameError(true);
      return;
    }
    createChannel.mutate(
      { name, lark_chat_id: newLarkChatId.trim() || undefined },
      {
        onSuccess: (channel: Channel) => {
          selectChannel(channel.id);
          setNewName("");
          setNewLarkChatId("");
          setCreateNameError(false);
          setCreateOpen(false);
        },
        onError: (err) => {
          // Duplicate (workspace, name) comes back as a 409 with a stable code;
          // localise off the code, not the server's English string.
          toast.error(
            isChannelNameTakenError(err)
              ? t(($) => $.sidebar.create_name_taken)
              : t(($) => $.sidebar.create_failed),
          );
        },
      },
    );
  };

  // Select a channel and reflect it in the URL so the address is shareable.
  // Clears any DM selection — the two regions are mutually exclusive.
  const selectChannel = (id: string) => {
    resetSidePanelState();
    setActiveDmId(null);
    setActiveId(id);
    replace(wsPaths.channelDetail(id));
  };

  // Select a DM (from the DIRECT MESSAGES region). Clears the group selection
  // and reflects the DM in the URL so it can be shared / deep-linked.
  const selectDm = (dm: DMItem) => {
    resetSidePanelState();
    setActiveId(null);
    setActiveDmId(dm.id);
    replace(wsPaths.channelDetail(dm.id));
  };

  // "Send message" entry point on a channel member row: create-or-find the DM
  // with that member and open it in place (we're already on the Messages view,
  // so no navigation round-trip is needed — selectDm switches the detail pane).
  const openDmWithMember = (member: ChannelMember) => {
    createOrFindDm.mutate(
      { peer_type: member.member_type, peer_id: member.member_id },
      {
        onSuccess: (dm) => selectDm(dm),
        onError: () => toast.error(t(($) => $.dm.open_failed)),
      },
    );
  };

  // Mobile-only: return from the detail (group or DM) to the list. Clears both
  // selections (so the list renders) and drops the deep-link param.
  const mobileBackToList = () => {
    resetSidePanelState();
    setActiveId(null);
    setActiveDmId(null);
    setMobilePanel(null);
    replace(wsPaths.channels());
  };

  const handleDelete = () => {
    const target = deleteTarget;
    if (!target) return;
    deleteChannel.mutate(target.id, {
      onSuccess: () => {
        toast.success(t(($) => $.delete_dialog.toast_success));
        // If the open channel was the one removed, drop the selection so the
        // `active` memo falls back to the first remaining channel.
        if (target.id === activeId) setActiveId(null);
        setDeleteTarget(null);
      },
      onError: () => toast.error(t(($) => $.delete_dialog.toast_failed)),
    });
  };

  const handleArchive = () => {
    const target = archiveTarget;
    if (!target) return;
    archiveChannel.mutate(target.id, {
      onSuccess: () => {
        if (target.id === activeId) setActiveId(null);
        setArchiveTarget(null);
      },
      onError: () => toast.error(t(($) => $.archive_dialog.error)),
    });
  };

  const handleRestoreChannel = (channelId: string) => {
    restoreChannel.mutate(channelId, {
      onError: () => toast.error(t(($) => $.archive_dialog.restore_error)),
    });
  };

  const handleStopChannelTask = useCallback(async (task: ChannelActiveTask) => {
    if (!active?.id) return;
    setStoppingChannelTaskId(task.task_id);
    try {
      await api.cancelTaskById(task.task_id);
      toast.success(t(($) => $.agent_status.stop_success, { name: task.agent_name }));
      qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(active.id) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    } catch {
      toast.error(t(($) => $.agent_status.stop_failed));
    } finally {
      setStoppingChannelTaskId((current) => (current === task.task_id ? null : current));
    }
  }, [active?.id, qc, t, wsId]);

  const handleToggleChannelPin = (channel: Channel) => {
    setChannelPin.mutate(
      { channelId: channel.id, pinned: !channel.pinned_at },
      { onError: () => toast.error(t(($) => $.dm.action_failed)) },
    );
  };

  const handleMarkChannelUnread = (channelId: string) => {
    markChannelUnread.mutate(channelId, {
      onError: () => toast.error(t(($) => $.dm.action_failed)),
    });
  };

  const muteChannel = useMuteChannel();

  const handleToggleChannelMute = (channel: Channel) => {
    muteChannel.mutate(
      { channelId: channel.id, muted: !isConversationMuted(channel) },
      { onError: () => toast.error(t(($) => $.dm.action_failed)) },
    );
  };

  const handleShare = async () => {
    if (!active) return;
    const url = getShareableUrl(wsPaths.channelDetail(active.id));
    try {
      await navigator.clipboard.writeText(url);
      toast.success(t(($) => $.share.copied));
    } catch {
      toast.error(t(($) => $.share.copy_failed));
    }
  };

  const publishTyping = (isTyping: boolean) => {
    if (!active) return;
    setTyping.mutate({ channelId: active.id, isTyping });
  };

  const scheduleTypingStop = () => {
    if (typingStopTimerRef.current) clearTimeout(typingStopTimerRef.current);
    typingStopTimerRef.current = setTimeout(() => {
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }, 1200);
  };

  const scheduleTypingPulse = () => {
    if (typingPulseTimerRef.current) clearTimeout(typingPulseTimerRef.current);
    typingPulseTimerRef.current = setTimeout(() => {
      if (typingStartedRef.current) {
        publishTyping(true);
        scheduleTypingPulse();
      }
    }, 3500);
  };

  const handleEditorUpdate = (value: string) => {
    if (!active || !activeDraftKey) return;
    setConversationDraft(activeDraftKey, value);
    if (value.trim()) {
      if (!typingStartedRef.current) {
        typingStartedRef.current = true;
        publishTyping(true);
        scheduleTypingPulse();
      }
      scheduleTypingStop();
      return;
    }
    if (typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
  };

  const handleThreadEditorUpdate = (value: string) => {
    setThreadDraftEmpty(!value.trim());
  };

  // Upload a file against the active channel and remember its URL → id so the
  // attachment binds to the message on send. The editor inserts the markdown
  // link itself; we only track the mapping.
  const handleUpload = useCallback(
    async (file: File): Promise<UploadResult | null> => {
      if (!active) return null;
      const result = await uploadWithToast(file, { channelId: active.id });
      if (result) {
        uploadMapRef.current.set(result.markdownLink || result.link, result.id);
      }
      return result;
    },
    [active, uploadWithToast],
  );

  const handlePickFiles = (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) {
      editorRef.current?.uploadFile(file);
    }
  };

  const handleThreadUpload = useCallback(
    async (file: File): Promise<UploadResult | null> => {
      if (!active) return null;
      const result = await uploadWithToast(file, { channelId: active.id });
      if (result) {
        threadUploadMapRef.current.set(result.markdownLink || result.link, result.id);
      }
      return result;
    },
    [active, uploadWithToast],
  );

  const handlePickThreadFiles = (files: FileList | null) => {
    if (!files) return;
    for (const file of Array.from(files)) {
      threadEditorRef.current?.uploadFile(file);
    }
  };

  const handleSend = () => {
    const content = editorRef.current?.getMarkdown()?.trim();
    // Empty-content early-return runs BEFORE the in-flight guard: after a send
    // succeeds, onSuccess clears the editor and onSettled releases the guard —
    // a still-held Enter in that gap grabs empty content and stops here.
    if (!content || !active) return;
    // Block while an upload is still in flight — otherwise the attachment id
    // isn't in uploadMapRef yet and the file would only bind to the channel,
    // not the message.
    if (editorRef.current?.hasActiveUploads()) return;
    // Only bind attachments still referenced in the body — edits that removed
    // the markdown link also drop the binding.
    const attachmentIds: string[] = [];
    for (const [url, id] of uploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    // Send lock (N held/auto-repeat Enter → 1 request) + payload-bound
    // client_message_id + the 3-way outcome, all owned by useComposerSend.
    const replyToMessageId = channelQuote?.id ?? null;
    const dispatched = channelSend.send({
      payloadKey: composePayloadKey(content, attachmentIds, replyToMessageId ?? ""),
      buildVars: (clientMessageId) => ({
        channelId: active.id,
        content,
        attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
        replyToMessageId,
        clientMessageId,
      }),
      mutate: sendMessage.mutate,
      onCommitted: () => {
        editorRef.current?.clearContent();
        uploadMapRef.current.clear();
        setChannelQuote(null);
        if (activeDraftKey) storeClearComposerDraft(activeDraftKey);
      },
      // 200-dedup is silent (onCommitted); a 409 or any other failure always
      // surfaces — the draft is kept, but the user must know this send did NOT
      // land (a silent 409 reads as a sent message).
      onVisibleError: () => toast.error(t(($) => $.composer.send_failed)),
    });
    if (dispatched && typingStartedRef.current) {
      typingStartedRef.current = false;
      publishTyping(false);
    }
  };

  const handleThreadSend = () => {
    const content = threadEditorRef.current?.getMarkdown()?.trim();
    if (!content || !active || !threadRoot) return;
    if (threadEditorRef.current?.hasActiveUploads()) return;
    const attachmentIds: string[] = [];
    for (const [url, id] of threadUploadMapRef.current) {
      if (content.includes(url)) attachmentIds.push(id);
    }
    const replyToMessageId = threadQuote?.id ?? null;
    threadSend.send({
      payloadKey: composePayloadKey(content, attachmentIds, `${threadRoot.id}:${replyToMessageId ?? ""}`),
      buildVars: (clientMessageId) => ({
        channelId: active.id,
        messageId: threadRoot.id,
        content,
        attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
        replyToMessageId,
        clientMessageId,
      }),
      mutate: sendThreadMessage.mutate,
      onCommitted: () => {
        threadEditorRef.current?.clearContent();
        threadUploadMapRef.current.clear();
        setThreadQuote(null);
        setThreadDraftEmpty(true);
      },
      onVisibleError: () => toast.error(t(($) => $.thread.send_failed)),
    });
  };

  const handleOpenThread = (message: ChannelMessage) => {
    focusThreadComposerOnOpenRef.current = true;
    setSelectedAgentPanelId(null);
    setOpenThreadRoot(message);
  };

  const handleQuoteMessage = (message: ChannelMessage) => {
    if (message.thread_root_message_id) {
      if (!threadRoot || threadRoot.id !== message.thread_root_message_id) return;
      setThreadQuote(message);
      threadEditorRef.current?.focus();
      return;
    }
    setChannelQuote(message);
    editorRef.current?.focus();
  };

  const handleOpenAgentPanel = (agentId: string) => {
    setOpenThreadRoot(null);
    setSelectedAgentPanelId(agentId);
  };

  const toggleInvite = (key: string) => {
    setSelectedInvites((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const inviteSelected = () => {
    if (!active || selectedInvites.size === 0) return;
    const members = Array.from(selectedInvites).map((key) => {
      const sep = key.indexOf(":");
      return {
        member_type: key.slice(0, sep) as "user" | "agent",
        member_id: key.slice(sep + 1),
      };
    });
    addMembers.mutate(
      { channelId: active.id, members },
      { onSuccess: () => setSelectedInvites(new Set()) },
    );
  };

  // Member management body (invite / members tabs + search). Extracted so the
  // SAME markup renders in the desktop header Popover and the mobile overflow
  // Drawer — no logic or layout duplicated. Guarded on `active` so the member-
  // remove handler always has a channel id.
  const memberPanelBody = active ? (
    <>
      <div className="flex border-b">
        <button
          type="button"
          onClick={() => setMemberTab("invite")}
          className={cn(
            "flex-1 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
            memberTab === "invite"
              ? "border-primary text-foreground"
              : "border-transparent text-muted-foreground hover:text-foreground",
          )}
        >
          {t(($) => $.members.tab_invite)}
        </button>
        <button
          type="button"
          onClick={() => setMemberTab("members")}
          className={cn(
            "flex-1 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
            memberTab === "members"
              ? "border-primary text-foreground"
              : "border-transparent text-muted-foreground hover:text-foreground",
          )}
        >
          {t(($) => $.members.tab_members)} · {channelMembers.length}
        </button>
      </div>
      <div className="p-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={memberQuery}
            onChange={(e) => setMemberQuery(e.target.value)}
            placeholder={t(($) => $.members.search)}
            className="h-8 pl-7"
          />
        </div>
      </div>
      {memberTab === "invite" ? (
        <>
          <div className="max-h-64 overflow-y-auto px-1.5 pb-1.5">
            {inviteCandidates.length === 0 ? (
              <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                {memberQuery
                  ? t(($) => $.members.no_results)
                  : t(($) => $.members.no_candidates)}
              </p>
            ) : (
              inviteCandidates.map((c) => (
                <label
                  key={c.key}
                  className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent"
                >
                  <Checkbox
                    checked={selectedInvites.has(c.key)}
                    onCheckedChange={() => toggleInvite(c.key)}
                  />
                  <ActorAvatar
                    name={c.presentation.displayName}
                    initials={initialsOf(c.presentation.displayName || "?")}
                    isAgent={c.type === "agent"}
                    size={26}
                    tint={c.type === "agent" ? agentColor(c.id) : undefined}
                  />
                  <ActorIdentityRow
                    displayName={c.presentation.displayName}
                    handle={c.presentation.handle}
                    showHandle={c.presentation.showHandleLabel}
                    primaryClassName="truncate text-sm"
                  />
                </label>
              ))
            )}
          </div>
          {selectedInvites.size > 0 && (
            <div className="border-t p-2">
              <Button
                size="sm"
                className="w-full"
                onClick={inviteSelected}
                disabled={addMembers.isPending}
              >
                {t(($) => $.members.invite_count, { count: selectedInvites.size })}
              </Button>
            </div>
          )}
        </>
      ) : (
        <div className="max-h-72 overflow-y-auto px-1.5 pb-2">
          {filteredMembers.length === 0 ? (
            <p className="px-2 py-6 text-center text-xs text-muted-foreground">
              {channelMembers.length === 0
                ? t(($) => $.members.empty)
                : t(($) => $.members.no_results)}
            </p>
          ) : (
            filteredMembers.map((m) => {
              const isAgent = m.member_type === "agent";
              const presentation = resolveActorIdentityPresentation(
                m,
                isAgent
                  ? t(($) => $.message.agent_badge)
                  : t(($) => $.members.title),
              );
              return (
                <div
                  key={`${m.member_type}:${m.member_id}`}
                  className="group flex items-center gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent"
                >
                  <ActorAvatar
                    name={presentation.displayName}
                    initials={initialsOf(presentation.displayName || "?")}
                    isAgent={isAgent}
                    size={26}
                    tint={isAgent ? agentColor(m.member_id) : undefined}
                  />
                  <ActorIdentityRow
                    displayName={presentation.displayName}
                    handle={presentation.handle}
                    showHandle={presentation.showHandleLabel}
                    primaryClassName="truncate text-sm"
                  />
                  {/* Send message: agents always, users except yourself (the
                      backend rejects a self-DM). Create-or-find then open it. */}
                  {(isAgent || m.member_id !== currentUserId) && (
                    <button
                      type="button"
                      onClick={() => openDmWithMember(m)}
                      disabled={createOrFindDm.isPending}
                      aria-label={t(($) => $.dm.send_message)}
                      title={t(($) => $.dm.send_message)}
                      className="rounded p-1 text-muted-foreground opacity-0 transition hover:text-foreground group-hover:opacity-100 disabled:opacity-50"
                    >
                      <MessageSquare className="size-3.5" />
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() =>
                      removeMember.mutate({
                        channelId: active.id,
                        memberType: m.member_type,
                        memberId: m.member_id,
                      })
                    }
                    aria-label={t(($) => $.members.remove_aria)}
                    className="rounded p-1 text-muted-foreground opacity-0 transition hover:text-destructive group-hover:opacity-100"
                  >
                    <X className="size-3.5" />
                  </button>
                </div>
              );
            })
          )}
        </div>
      )}
    </>
  ) : null;

  // Shared channel row for the unified PINNED section and the CHANNELS list.
  const renderChannelSidebarRow = (channel: Channel) => {
    const realUnread = channel.real_unread_count ?? channel.unread_count ?? 0;
    const isManualDot = !!channel.manually_unread && realUnread === 0;
    const isMuted = isConversationMuted(channel);
    const last = channel.last_message;
    const preview = last
      ? formatChannelMessagePreview(
          resolveChannelAuthorDisplayName(last, {
            members: workspaceMembers,
            agents,
          }),
          last.content,
          resolveMentionPreview,
          last.parts,
        )
      : "";
    const pinned = !!channel.pinned_at;
    const archiveAllowed = canArchive(channel);
    const channelMenuItems = (
      <>
        <ContextMenuItem onClick={() => handleMarkChannelUnread(channel.id)}>
          <Mail className="size-4" />
          {t(($) => $.sidebar.mark_unread)}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => handleToggleChannelPin(channel)}>
          {pinned ? <PinOff className="size-4" /> : <Pin className="size-4" />}
          {pinned ? t(($) => $.sidebar.unpin) : t(($) => $.sidebar.pin)}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => handleToggleChannelMute(channel)}>
          {isMuted ? <Bell className="size-4" /> : <BellOff className="size-4" />}
          {isMuted ? t(($) => $.sidebar.unmute) : t(($) => $.sidebar.mute)}
        </ContextMenuItem>
        <ContextMenuSeparator />
        {archiveAllowed ? (
          <ContextMenuItem onClick={() => setArchiveTarget(channel)}>
            <Archive className="size-4" />
            {t(($) => $.sidebar.archive)}
          </ContextMenuItem>
        ) : (
          <Tooltip>
            <TooltipTrigger>
              <ContextMenuItem
                aria-disabled
                onClick={() => toast.error(t(($) => $.sidebar.archive_permission))}
                className="opacity-50"
              >
                <Archive className="size-4" />
                {t(($) => $.sidebar.archive)}
              </ContextMenuItem>
            </TooltipTrigger>
            <TooltipContent side="right">
              {t(($) => $.sidebar.archive_permission)}
            </TooltipContent>
          </Tooltip>
        )}
      </>
    );
    return (
      <ContextMenu key={channel.id}>
        <ContextMenuTrigger
          render={
            <div
              data-pinned={pinned ? "true" : undefined}
              className={cn(
                "group/row relative mb-0.5 rounded-lg transition-colors",
                active?.id === channel.id ? "bg-primary/[0.08]" : "hover:bg-accent",
              )}
            />
          }
        >
          <button
            type="button"
            onClick={() => selectChannel(channel.id)}
            className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left"
          >
            <ChannelGroupAvatar members={channel.members ?? []} size={40} />
            <div className="min-w-0 flex-1">
              <div className="flex items-center justify-between gap-2">
                <span className="flex min-w-0 items-center gap-1 truncate text-sm font-medium text-foreground">
                  {pinned && (
                    <Pin className="size-3 shrink-0 -rotate-45 fill-muted-foreground/70 text-muted-foreground/70" />
                  )}
                  <span className="truncate">{channel.name}</span>
                  {isMuted && (
                    <MutedIndicator label={t(($) => $.sidebar.muted_label)} />
                  )}
                  {channel.lark_chat_id && (
                    <Smartphone className="size-3 shrink-0 text-emerald-600" />
                  )}
                </span>
                {last && (
                  <span className="shrink-0 text-[11px] text-muted-foreground">
                    {timeAgo(last.created_at)}
                  </span>
                )}
              </div>
              <div className="mt-0.5 flex items-center justify-between gap-2">
                <span className="truncate text-xs text-muted-foreground">{preview}</span>
                <ConversationUnreadAffordance
                  realUnread={realUnread}
                  isManualDot={isManualDot}
                  isMuted={isMuted}
                  hasMention={!!channel.has_mention}
                  mentionLabel={t(($) => $.sidebar.mention_indicator)}
                />
              </div>
            </div>
          </button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <button
                  type="button"
                  aria-label={t(($) => $.sidebar.menu_aria)}
                  className="absolute right-1 top-1.5 flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus-visible:opacity-100 group-hover/row:opacity-100 data-[popup-open]:opacity-100"
                >
                  <MoreHorizontal className="size-4" />
                </button>
              }
            />
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => handleMarkChannelUnread(channel.id)}>
                <Mail className="size-4" />
                {t(($) => $.sidebar.mark_unread)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => handleToggleChannelPin(channel)}>
                {pinned ? <PinOff className="size-4" /> : <Pin className="size-4" />}
                {pinned ? t(($) => $.sidebar.unpin) : t(($) => $.sidebar.pin)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => handleToggleChannelMute(channel)}>
                {isMuted ? <Bell className="size-4" /> : <BellOff className="size-4" />}
                {isMuted ? t(($) => $.sidebar.unmute) : t(($) => $.sidebar.mute)}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {archiveAllowed ? (
                <DropdownMenuItem onClick={() => setArchiveTarget(channel)}>
                  <Archive className="size-4" />
                  {t(($) => $.sidebar.archive)}
                </DropdownMenuItem>
              ) : (
                <Tooltip>
                  <TooltipTrigger>
                    <DropdownMenuItem
                      aria-disabled
                      onClick={() => toast.error(t(($) => $.sidebar.archive_permission))}
                      className="opacity-50"
                    >
                      <Archive className="size-4" />
                      {t(($) => $.sidebar.archive)}
                    </DropdownMenuItem>
                  </TooltipTrigger>
                  <TooltipContent side="left">
                    {t(($) => $.sidebar.archive_permission)}
                  </TooltipContent>
                </Tooltip>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </ContextMenuTrigger>
        <ContextMenuContent>
          {channelMenuItems}
        </ContextMenuContent>
      </ContextMenu>
    );
  };

  // Channel list pane. Full-width on mobile (list-first); a 280px sidebar on
  // desktop. The `border-r` only makes sense beside the stream pane, so it's
  // dropped on mobile where the list stands alone.
  const listPane = (
    <aside
      className={cn(
        "flex flex-1 min-h-0 flex-col bg-muted/20",
        isMobile ? "min-w-0" : "border-r",
      )}
    >
          <div className="flex items-center gap-2 px-4 pb-1 pt-4">
            <MobileSidebarTrigger />
            <h2 className="flex-1 text-lg font-semibold">{t(($) => $.sidebar.heading)}</h2>
          </div>
          <div className="px-3 pb-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t(($) => $.sidebar.search)}
                className="h-9 pl-8"
              />
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
            {/* Unified PINNED section (Slack Starred / 置顶分组) — DMs + channels */}
            <PinnedConversationsSection entries={filteredPinnedEntries}>
              {filteredPinnedEntries.map((entry) => {
                if (entry.kind === "dm") {
                  const dm = entry.dm;
                  return (
                    <DmConversationRow
                      key={`pinned-dm:${dm.source}:${dm.id}`}
                      dm={dm}
                      active={activeDmId === dm.id}
                      currentUserName={currentUserName}
                      timeAgo={timeAgo}
                      resolveMentionPreview={resolveMentionPreview}
                      members={workspaceMembers}
                      agents={agents}
                      onSelect={() => selectDm(dm)}
                      onTogglePin={() => dmActions.togglePin(dm)}
                      onMarkUnread={() => dmActions.markUnread(dm)}
                      onToggleMute={() => dmActions.toggleMute(dm)}
                      onClose={() => dmActions.close(dm)}
                    />
                  );
                }
                const channel = entry.channel;
                return renderChannelSidebarRow(channel);
              })}
            </PinnedConversationsSection>

            <DmList
              activeId={activeDmId}
              currentUserName={currentUserName}
              searchQuery={search}
              onSelect={selectDm}
            />
            {/* CHANNELS section — collapsible, mirrors DIRECT MESSAGES layout */}
            <div className="mt-1">
              <div className="flex items-center gap-0.5 px-2 py-1.5">
                <button
                  type="button"
                  onClick={() => setChannelsCollapsed((c) => !c)}
                  className="flex flex-1 items-center gap-1 text-xs font-medium uppercase tracking-wide text-muted-foreground transition-colors hover:text-foreground"
                  aria-expanded={!channelsCollapsed}
                >
                  {channelsCollapsed ? (
                    <ChevronRight className="size-3.5 shrink-0" />
                  ) : (
                    <ChevronDown className="size-3.5 shrink-0" />
                  )}
                  <span className="flex-1 text-left">{t(($) => $.sidebar.groups)}</span>
                  {channelsCollapsed && aggregateChannelUnread > 0 && (
                    <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
                      {aggregateChannelUnread > 99 ? "99+" : aggregateChannelUnread}
                    </span>
                  )}
                </button>
                {/* Create channel "+" moved from top heading to here */}
                <Popover
                  open={createOpen}
                  onOpenChange={(open) => {
                    setCreateOpen(open);
                    if (!open) setCreateNameError(false);
                  }}
                >
                  <PopoverTrigger
                    render={
                      <button
                        type="button"
                        aria-label={t(($) => $.sidebar.create_aria)}
                        className="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                      />
                    }
                  >
                    <Plus className="size-4" />
                  </PopoverTrigger>
                  <PopoverContent align="start" className="w-72 space-y-2">
                    <Input
                      placeholder={t(($) => $.sidebar.name_placeholder)}
                      value={newName}
                      aria-invalid={createNameError}
                      onChange={(e) => {
                        setNewName(e.target.value);
                        if (createNameError) setCreateNameError(false);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") handleCreate();
                      }}
                    />
                    {createNameError && (
                      <p className="text-xs text-destructive">{t(($) => $.sidebar.name_required)}</p>
                    )}
                    <Input
                      placeholder={t(($) => $.sidebar.lark_placeholder)}
                      value={newLarkChatId}
                      onChange={(e) => setNewLarkChatId(e.target.value)}
                    />
                    <Button className="w-full" onClick={handleCreate} disabled={createChannel.isPending}>
                      {t(($) => $.sidebar.create_aria)}
                    </Button>
                  </PopoverContent>
                </Popover>
              </div>

              {!channelsCollapsed && (
                isLoading ? (
                  <div className="space-y-2 p-2">
                    <Skeleton className="h-12" />
                    <Skeleton className="h-12" />
                  </div>
                ) : hasSidebarSearch && filteredChannels.length === 0 && unpinnedChannels.length > 0 ? (
                  <div className="space-y-1 px-3 py-4 text-xs text-muted-foreground">
                    <p className="font-medium text-foreground">
                      {t(($) => $.sidebar.no_conversation_matches)}
                    </p>
                    <p>{t(($) => $.sidebar.search_scope_hint)}</p>
                  </div>
                ) : channels.length === 0 ? (
                  <div className="p-3 text-sm text-muted-foreground">{t(($) => $.sidebar.empty)}</div>
                ) : (
                  filteredChannels.map((channel) => renderChannelSidebarRow(channel))
                )
              )}

              {/* Archived (N) — only shown when there are archived channels */}
              {archivedChannels.length > 0 && (
                <div className="mt-1">
                  <button
                    type="button"
                    onClick={() => setArchivedOpen((o) => !o)}
                    className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
                  >
                    {archivedOpen ? (
                      <ChevronDown className="size-3.5 shrink-0" />
                    ) : (
                      <ChevronRight className="size-3.5 shrink-0" />
                    )}
                    <Archive className="size-3 shrink-0" />
                    <span className="flex-1 text-left">
                      {t(($) => $.sidebar.archived_section)} ({archivedChannels.length})
                    </span>
                  </button>
                  {archivedOpen &&
                    archivedChannels.map((channel) => {
                      const restoreAllowed = canArchive(channel);
                      return (
                        <ContextMenu key={channel.id}>
                          <ContextMenuTrigger
                            render={
                              <div className="group/archived relative mb-0.5 rounded-lg transition-colors hover:bg-accent" />
                            }
                          >
                            <button
                              type="button"
                              onClick={() => selectChannel(channel.id)}
                              className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left opacity-60 hover:opacity-100"
                            >
                              <ChannelGroupAvatar members={channel.members ?? []} size={40} />
                              <div className="min-w-0 flex-1">
                                <span className="truncate text-sm font-medium text-muted-foreground">
                                  {channel.name}
                                </span>
                              </div>
                            </button>
                            <DropdownMenu>
                              <DropdownMenuTrigger
                                render={
                                  <button
                                    type="button"
                                    aria-label={t(($) => $.sidebar.menu_aria)}
                                    className="absolute right-1 top-1.5 flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus-visible:opacity-100 group-hover/archived:opacity-100 data-[popup-open]:opacity-100"
                                  >
                                    <MoreHorizontal className="size-4" />
                                  </button>
                                }
                              />
                              <DropdownMenuContent align="end">
                                {restoreAllowed ? (
                                  <DropdownMenuItem onClick={() => handleRestoreChannel(channel.id)}>
                                    <ArchiveRestore className="size-4" />
                                    {t(($) => $.sidebar.archived_restore)}
                                  </DropdownMenuItem>
                                ) : (
                                  <Tooltip>
                                    <TooltipTrigger>
                                      <DropdownMenuItem
                                        aria-disabled
                                        onClick={() => toast.error(t(($) => $.sidebar.restore_permission))}
                                        className="opacity-50"
                                      >
                                        <ArchiveRestore className="size-4" />
                                        {t(($) => $.sidebar.archived_restore)}
                                      </DropdownMenuItem>
                                    </TooltipTrigger>
                                    <TooltipContent side="left">
                                      {t(($) => $.sidebar.restore_permission)}
                                    </TooltipContent>
                                  </Tooltip>
                                )}
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </ContextMenuTrigger>
                          <ContextMenuContent>
                            {restoreAllowed ? (
                              <ContextMenuItem onClick={() => handleRestoreChannel(channel.id)}>
                                <ArchiveRestore className="size-4" />
                                {t(($) => $.sidebar.archived_restore)}
                              </ContextMenuItem>
                            ) : (
                              <Tooltip>
                                <TooltipTrigger>
                                  <ContextMenuItem
                                    aria-disabled
                                    onClick={() => toast.error(t(($) => $.sidebar.restore_permission))}
                                    className="opacity-50"
                                  >
                                    <ArchiveRestore className="size-4" />
                                    {t(($) => $.sidebar.archived_restore)}
                                  </ContextMenuItem>
                                </TooltipTrigger>
                                <TooltipContent side="right">
                                  {t(($) => $.sidebar.restore_permission)}
                                </TooltipContent>
                              </Tooltip>
                            )}
                          </ContextMenuContent>
                        </ContextMenu>
                      );
                    })}
                </div>
              )}
            </div>
          </div>
    </aside>
  );

  // Channel detail pane: header + message stream + composer. On mobile it
  // takes the full width and grows a Back button into the header so the user
  // can return to the list.
  const showChannelDetailSkeleton =
    isLoading || (!!activeId && !activeDmId && !active);
  // The thread surface is the shared <ThreadPanel> (pinned root + flat replies +
  // participant chips + wake strip), fed the #251 read-model off the root
  // message. also-send is CUT this round (#256), so no also-send props are
  // passed — the panel then hides the checkbox entirely.
  const threadPanel =
    active && threadRoot ? (
      <ThreadPanel
        root={threadRoot}
        replies={threadReplies}
        currentUserId={currentUserId}
        currentUserName={currentUserName ?? undefined}
        wakeAnnotations={threadWakeAnnotations}
        isMobile={isMobile}
        onBack={() => setOpenThreadRoot(null)}
        onViewParent={() => {
          setHighlightMessageId(threadRoot.id);
          if (isMobile) setOpenThreadRoot(null);
        }}
        loading={threadLoading}
        loadError={threadError}
        onRetry={() => refetchThread()}
        onReact={handleReactToMessage}
        onQuote={handleQuoteMessage}
        editor={
          <ContentEditor
            key={`thread-editor:${threadRoot.id}`}
            ref={threadEditorRef}
            placeholder={t(($) => $.thread.composer_placeholder)}
            onUpdate={handleThreadEditorUpdate}
            onSubmit={handleThreadSend}
            onUploadFile={handleThreadUpload}
            submitOnEnter
            showBubbleMenu={false}
            mentionAllowedActorIds={channelMemberIds}
          />
        }
        onSend={handleThreadSend}
        sendDisabled={threadDraftEmpty}
        sending={sendThreadMessage.isPending}
        prefix={threadQuote ? (
          <ComposerQuotePreview
            message={threadQuote}
            currentUserId={currentUserId}
            ownName={currentUserName ?? undefined}
            onCancel={() => setThreadQuote(null)}
          />
        ) : undefined}
        composerLeadingActions={
          <>
            <input
              ref={threadFileInputRef}
              type="file"
              multiple
              className="hidden"
              onChange={(e) => {
                handlePickThreadFiles(e.target.files);
                e.target.value = "";
              }}
            />
            <Button
              variant="ghost"
              size="icon"
              className={cn(isMobile ? "size-10" : "size-8")}
              aria-label={t(($) => $.composer.attach_aria)}
              onClick={() => threadFileInputRef.current?.click()}
            >
              <Paperclip className={cn(isMobile ? "size-5" : "size-4")} />
            </Button>
          </>
        }
        readOnly={isActiveArchived}
        readOnlyContent={
          <>
            <Archive className="size-4 shrink-0" />
            <span>{t(($) => $.archive_dialog.readonly_notice)}</span>
          </>
        }
        activitySlot={
          <ConversationActivityStrip
            tasks={activeTasks}
            stoppingTaskId={stoppingChannelTaskId}
            onStopTask={handleStopChannelTask}
          />
        }
      />
    ) : null;
  const agentPanel =
    active && selectedAgentPanel ? (
      <AgentSidePanel
        agent={selectedAgentPanel}
        currentUserId={currentUserId}
        members={workspaceMembers}
        onClose={() => setSelectedAgentPanelId(null)}
      />
    ) : null;
  const channelConversationPane = (
    <main className="relative flex flex-1 min-h-0 min-w-0 flex-col bg-background">
      {!active ? (
        showChannelDetailSkeleton ? (
          <ConversationSwitchSkeleton isMobile={isMobile} />
        ) : (
          <EmptyState onCreate={() => setCreateOpen(true)} />
        )
      ) : (
        <>
          <ConversationHeader
            isMobile={isMobile}
            leading={
              <>
                {isMobile && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-10 shrink-0 text-muted-foreground"
                    aria-label={t(($) => $.header.back)}
                    onClick={mobileBackToList}
                  >
                    <ArrowLeft className="size-5" />
                  </Button>
                )}
                <ChannelGroupAvatar members={channelMembers} size={28} />
              </>
            }
            title={active.name}
            meta={
              <>
                {t(($) => $.header.running)}
                {rosterSummary ? ` · ${rosterSummary}` : ""}
              </>
            }
            badges={
              <>
                {isConversationMuted(active) && (
                  <MutedIndicator label={t(($) => $.sidebar.muted_label)} />
                )}
                {isActiveArchived && (
                  <Badge variant="secondary" className="shrink-0 uppercase tracking-wide">
                    {t(($) => $.sidebar.archived_section)}
                  </Badge>
                )}
                {active.lark_chat_id && (
                  <Badge variant="secondary" className="shrink-0">
                    {t(($) => $.header.feishu)}
                  </Badge>
                )}
              </>
            }
            actions={isMobile ? (
              // Mobile: collapse members / share / stats / files into a
              // single "⋯" that opens the bottom Drawer's action menu.
              // size-10 keeps the tap target ≥44px.
              <Button
                variant="ghost"
                size="icon"
                className="size-10 shrink-0 text-muted-foreground"
                aria-label={t(($) => $.header.more_aria)}
                onClick={() => setMobilePanel("menu")}
              >
                <MoreHorizontal className="size-5" />
              </Button>
            ) : (
              <>
                <Popover>
                  <PopoverTrigger
                    className="flex items-center gap-1.5 rounded-full p-0.5 transition-colors hover:bg-accent"
                    aria-label={t(($) => $.header.manage_members_aria)}
                  >
                    <MemberStack members={channelMembers} />
                    <span className="flex size-7 items-center justify-center rounded-full border border-dashed text-muted-foreground">
                      <Plus className="size-3.5" />
                    </span>
                  </PopoverTrigger>
                  <PopoverContent align="end" className="w-80 p-0">
                    {memberPanelBody}
                  </PopoverContent>
                </Popover>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-8"
                    aria-label={t(($) => $.conv_search.search_aria)}
                    onClick={() => setConvSearchOpen(true)}
                  >
                    <Search className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-8"
                    aria-label={t(($) => $.header.share_aria)}
                    onClick={handleShare}
                  >
                    <Share2 className="size-4" />
                  </Button>
                  <Popover>
                    <PopoverTrigger
                      className="flex size-8 items-center justify-center rounded-md transition-colors hover:bg-accent"
                      aria-label={t(($) => $.header.stats_aria)}
                    >
                      <PieChart className="size-4" />
                    </PopoverTrigger>
                    <PopoverContent align="end" className="w-72">
                      <p className="mb-3 text-sm font-medium">{t(($) => $.stats.title)}</p>
                      <ChannelStatsPanel channelId={active.id} />
                    </PopoverContent>
                  </Popover>
                  <Popover>
                    <PopoverTrigger
                      className="flex size-8 items-center justify-center rounded-md transition-colors hover:bg-accent"
                      aria-label={t(($) => $.header.files_aria)}
                    >
                      <FileText className="size-4" />
                    </PopoverTrigger>
                    <PopoverContent align="end" className="w-80">
                      <p className="mb-3 text-sm font-medium">{t(($) => $.files.title)}</p>
                      <ChannelFilesPanel channelId={active.id} />
                    </PopoverContent>
                  </Popover>
                </div>
              </>
            )}
          />
              {convSearchOpen && (
                <div
                  className={cn(
                    "flex items-center gap-2 border-b border-border/40 bg-muted/15 py-2",
                    isMobile ? "px-2" : "px-5",
                  )}
                >
                  <span className="shrink-0 rounded-md border bg-background px-2 py-1 text-xs text-muted-foreground">
                    {t(($) => $.conv_search.scope_current_messages)}
                  </span>
                  <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <input
                    type="search"
                    autoFocus
                    value={convSearchQuery}
                    onChange={(e) => setConvSearchQuery(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Escape") {
                        setConvSearchOpen(false);
                        setConvSearchQuery("");
                        setConvSearchResults([]);
                        setConvSearchTotal(0);
                        setConvSearchIndex(0);
                      }
                    }}
                    placeholder={t(($) => $.conv_search.group_placeholder)}
                    className="h-8 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                  />
                  {convSearchQuery.trim() && (
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {convSearchTotal === 0
                        ? t(($) => $.conv_search.no_results)
                        : t(($) => $.conv_search.result_count, {
                            current: convSearchIndex + 1,
                            total: convSearchTotal,
                          })}
                    </span>
                  )}
                  <div className="flex shrink-0 items-center gap-0.5">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      disabled={convSearchTotal === 0}
                      aria-label={t(($) => $.conv_search.prev_aria)}
                      onClick={() => setConvSearchIndex((i) => Math.max(0, i - 1))}
                    >
                      <ChevronUp className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      disabled={convSearchTotal === 0}
                      aria-label={t(($) => $.conv_search.next_aria)}
                      onClick={() => setConvSearchIndex((i) => Math.min(convSearchTotal - 1, i + 1))}
                    >
                      <ChevronDown className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      aria-label={t(($) => $.conv_search.close_aria)}
                      onClick={() => {
                        setConvSearchOpen(false);
                        setConvSearchQuery("");
                        setConvSearchResults([]);
                        setConvSearchTotal(0);
                        setConvSearchIndex(0);
                      }}
                    >
                      <X className="size-4" />
                    </Button>
                  </div>
                </div>
              )}

              {jumpStatus === "exhausted" && (
                <output className="block border-b bg-muted/40 px-5 py-1.5 text-center text-xs text-muted-foreground">
                  {t(($) => $.message_loading.jump_not_found)}
                </output>
              )}

              <ChannelMessageList
                key={active.id}
                messages={messages}
                currentUserId={currentUserId}
                ownName={currentUserName ?? undefined}
                highlightMessageId={effectiveHighlightId}
                lastReadSeq={dividerLastReadSeq}
                // #340 divider count, most-authoritative first: the around
                // response's server-computed total → the entry-frozen list count
                // → (in MessageViewport) the loaded-window count.
                unreadCount={
                  messagePages?.pages?.[0]?.unread_total ??
                  entryAnchor.unreadCount
                }
                firstItemIndex={messagesFirstItemIndex}
                searchHitIds={searchHitIds}
                searchQuery={searchHighlightQuery}
                loading={messagesLoading}
                loadingOlder={isFetchingOlderMessages}
                hasOlder={!!hasOlderMessages}
                onLoadOlder={() => fetchOlderMessages()}
                loadOlderLabel={t(($) => $.message_loading.load_older)}
                loadingOlderLabel={t(($) => $.message_loading.loading_older)}
                loadErrorLabel={messagesError ? t(($) => $.message_loading.load_failed_retry) : undefined}
                onRetry={() => refetchMessages()}
                emptyLabel={t(($) => $.thread.empty)}
                onOpenThread={isActiveArchived ? undefined : handleOpenThread}
                onScrollToMessage={setHighlightMessageId}
                onReact={handleReactToMessage}
                onQuoteMessage={isActiveArchived ? undefined : handleQuoteMessage}
                onEditMessage={isActiveArchived ? undefined : handleEditMessage}
                onDeleteMessage={isActiveArchived ? undefined : handleDeleteMessage}
                onOpenAgent={handleOpenAgentPanel}
              />

              {isActiveArchived ? (
                <ReadOnlyConversationBanner>
                  <Archive className="size-4 shrink-0" />
                  <span className="flex-1">{t(($) => $.archive_dialog.readonly_notice)}</span>
                  {canArchive(active) ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleRestoreChannel(active.id)}
                      disabled={restoreChannel.isPending}
                    >
                      <ArchiveRestore className="size-3.5" />
                      {t(($) => $.sidebar.archived_restore)}
                    </Button>
                  ) : (
                    <Tooltip>
                      <TooltipTrigger>
                        <Button
                          variant="outline"
                          size="sm"
                          aria-disabled
                          className="cursor-default opacity-50"
                          onClick={() => toast.error(t(($) => $.sidebar.restore_permission))}
                        >
                          <ArchiveRestore className="size-3.5" />
                          {t(($) => $.sidebar.archived_restore)}
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>{t(($) => $.sidebar.restore_permission)}</TooltipContent>
                    </Tooltip>
                  )}
                </ReadOnlyConversationBanner>
              ) : (
                <>
                  <ConversationActivityStrip
                    typingActors={activeTypingActors}
                    tasks={activeTasks}
                    stoppingTaskId={stoppingChannelTaskId}
                    onStopTask={handleStopChannelTask}
                  />
                  <Composer
                    surface="channel"
                    sendLabel={t(($) => $.composer.send)}
                    sendDisabled={activeDraftEmpty}
                    sending={sendMessage.isPending}
                    onSend={handleSend}
                    isMobile={isMobile}
                    prefix={channelQuote ? (
                      <ComposerQuotePreview
                        message={channelQuote}
                        currentUserId={currentUserId}
                        ownName={currentUserName ?? undefined}
                        onCancel={() => setChannelQuote(null)}
                      />
                    ) : undefined}
                    editor={
                      <ContentEditor
                        key={active.id}
                        ref={editorRef}
                        defaultValue={activeDraft}
                        placeholder={t(($) => $.composer.placeholder)}
                        onUpdate={handleEditorUpdate}
                        debounceMs={0}
                        onSubmit={handleSend}
                        onUploadFile={handleUpload}
                        submitOnEnter
                        showBubbleMenu={false}
                        enableIssueReferences
                        mentionAllowedActorIds={channelMemberIds}
                      />
                    }
                    leadingActions={
                      <>
                        <input
                          ref={fileInputRef}
                          type="file"
                          multiple
                          className="hidden"
                          onChange={(e) => {
                            handlePickFiles(e.target.files);
                            e.target.value = "";
                          }}
                        />
                        <Button
                          variant="ghost"
                          size="icon"
                          className={cn(isMobile ? "size-10" : "size-8")}
                          aria-label={t(($) => $.composer.attach_aria)}
                          onClick={() => fileInputRef.current?.click()}
                        >
                          <Paperclip className={cn(isMobile ? "size-5" : "size-4")} />
                        </Button>
                        <ProjectPickerButton
                          wsId={wsId}
                          value={channelProjectId || null}
                          onChange={(projectId) => setChannelProject.mutate(projectId)}
                          label={t(($) => $.composer.project_label)}
                          noneLabel={t(($) => $.composer.project_none)}
                          tooltip={t(($) => $.composer.project_tooltip)}
                        />
                        <Button
                          variant="ghost"
                          size="icon"
                          className={cn(isMobile ? "size-10" : "size-8")}
                          aria-label={t(($) => $.composer.issue_ref_aria)}
                          onClick={() => editorRef.current?.openIssueReferences()}
                        >
                          <Hash className={cn(isMobile ? "size-5" : "size-4")} />
                        </Button>
                      </>
                    }
                  />
                </>
              )}
            </>
          )}
        </main>
  );
  const detailPane = !isMobile ? (
    <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
      <ResizablePanel id="conversation" minSize="50%" className="flex min-h-0 flex-col">
        {channelConversationPane}
      </ResizablePanel>
      {threadPanel || agentPanel ? (
        <>
          <ResizableHandle />
          <ResizablePanel
            id={threadPanel ? "thread" : "agent-files"}
            defaultSize={440}
            minSize={360}
            maxSize={640}
            groupResizeBehavior="preserve-pixel-size"
            className="border-l border-border/30 bg-background"
          >
            {threadPanel ?? agentPanel}
          </ResizablePanel>
        </>
      ) : null}
    </ResizablePanelGroup>
  ) : (
    threadPanel ?? channelConversationPane
  );

  // DM detail pane — rendered in place of the group detail when a DM is active.
  // Visible direct messages use the R2 dm_channel surface; legacy sessions are
  // fail-closed inside DmConversation until the backend source is removed.
  // When a `?dm=` deep link opens cold, `activeDmId` is set before the DM list
  // resolves the row — keep the conversation structure in place instead of
  // dropping to a blank pane during that window.
  const dmDraftKey = activeDm ? (`dm:${activeDm.id}` as const) : null;
  const dmDetailPane = activeDm ? (
    <DmConversation
      key={`${activeDm.source}:${activeDm.id}`}
      dm={activeDm}
      onBack={mobileBackToList}
      draft={dmDraftKey ? (composerDrafts[dmDraftKey]?.content ?? "") : ""}
      onDraftChange={(value) => {
        if (dmDraftKey) setConversationDraft(dmDraftKey, value);
      }}
      onDraftClear={() => {
        if (dmDraftKey) storeClearComposerDraft(dmDraftKey);
      }}
    />
  ) : (
    <ConversationSwitchSkeleton isMobile={isMobile} />
  );

  // The detail surface: a selected DM wins over a group (selections are
  // mutually exclusive, but this also covers the deep-link-before-list-loads
  // window where `activeDmId` is set but the DM row hasn't resolved yet).
  const detailSurface = activeDmId ? dmDetailPane : detailPane;

  if (!viewportReady) {
    return <InitialChannelsShellSkeleton />;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {isMobile ? (
        // Mobile: single full-width column — the list, or (when a conversation
        // is active) the detail with a Back button. Matches the inbox
        // list↔detail pattern. DMs participate in the same switching via
        // `activeDmId`.
        //
        // Height is pinned to 100dvh (dynamic viewport height) rather than the
        // app-shell's flex height so the soft keyboard shrinks the viewport and
        // the composer stays above it: the message area is the flex child that
        // compresses (`min-h-0 flex-1 overflow-y-auto`, inside detailPane) and
        // the composer is the pinned last flex child (non-absolute). 100vh would
        // include the keyboard's area and push the composer off-screen.
        <MobileListDetailLayout
          className="h-[100dvh] min-h-0 min-w-0 bg-background"
          showDetail={!!(active || activeDmId)}
          list={listPane}
          detail={detailSurface}
        />
      ) : (
        <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>
          <ResizablePanel id="list" defaultSize={280} minSize={240} maxSize={480} groupResizeBehavior="preserve-pixel-size" className="flex min-h-0 flex-col">
            {listPane}
          </ResizablePanel>
          <ResizableHandle />
          <ResizablePanel id="detail" minSize="40%" className="flex min-h-0 flex-col">
            {detailSurface}
          </ResizablePanel>
        </ResizablePanelGroup>
      )}

      {/* Mobile overflow drawer. One bottom Drawer (vaul, with drag handle)
          behind the header "⋯": `"menu"` lists the actions (Members / Share /
          Stats / Files); picking one swaps the body to that section.
          Members/Stats/Files reuse the exact same component bodies as the
          desktop popovers. */}
      {isMobile && active && (
        <Drawer
          direction="bottom"
          open={mobilePanel !== null}
          onOpenChange={(open) => {
            if (!open) setMobilePanel(null);
          }}
        >
          <DrawerContent className="max-h-[85dvh] gap-0 overflow-y-auto p-0">
            <DrawerHeader className="flex-row items-center gap-1 border-b py-3">
              {mobilePanel !== "menu" && (
                <button
                  type="button"
                  onClick={() => setMobilePanel("menu")}
                  aria-label={t(($) => $.header.back)}
                  className="-ml-1 flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent"
                >
                  <ChevronLeft className="size-5" />
                </button>
              )}
              <DrawerTitle>
                {mobilePanel === "members"
                  ? t(($) => $.members.title)
                  : mobilePanel === "stats"
                    ? t(($) => $.stats.title)
                    : mobilePanel === "files"
                      ? t(($) => $.files.title)
                      : active.name}
              </DrawerTitle>
            </DrawerHeader>

            {mobilePanel === "menu" && (
              <div className="flex flex-col py-1">
                <button
                  type="button"
                  onClick={() => setMobilePanel("members")}
                  className="flex min-h-[44px] items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent"
                >
                  <Users className="size-5 shrink-0 text-muted-foreground" />
                  <span className="flex-1">{t(($) => $.header.manage_members_aria)}</span>
                  <span className="text-xs text-muted-foreground">{channelMembers.length}</span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setMobilePanel(null);
                    void handleShare();
                  }}
                  className="flex min-h-[44px] items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent"
                >
                  <Share2 className="size-5 shrink-0 text-muted-foreground" />
                  <span className="flex-1">{t(($) => $.header.share_aria)}</span>
                </button>
                <button
                  type="button"
                  onClick={() => setMobilePanel("stats")}
                  className="flex min-h-[44px] items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent"
                >
                  <PieChart className="size-5 shrink-0 text-muted-foreground" />
                  <span className="flex-1">{t(($) => $.stats.title)}</span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                </button>
                <button
                  type="button"
                  onClick={() => setMobilePanel("files")}
                  className="flex min-h-[44px] items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent"
                >
                  <FileText className="size-5 shrink-0 text-muted-foreground" />
                  <span className="flex-1">{t(($) => $.files.title)}</span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                </button>
              </div>
            )}

            {mobilePanel === "members" && <div>{memberPanelBody}</div>}
            {mobilePanel === "stats" && (
              <div className="p-4">
                <ChannelStatsPanel channelId={active.id} />
              </div>
            )}
            {mobilePanel === "files" && (
              <div className="p-4">
                <ChannelFilesPanel channelId={active.id} />
              </div>
            )}
          </DrawerContent>
        </Drawer>
      )}

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.delete_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.delete_dialog.description, { name: deleteTarget?.name ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.delete_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteChannel.isPending}
            >
              {t(($) => $.delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={archiveTarget !== null}
        onOpenChange={(open) => {
          if (!open) setArchiveTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.archive_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.archive_dialog.description, { name: archiveTarget?.name ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.archive_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleArchive}
              disabled={archiveChannel.isPending}
            >
              {t(($) => $.archive_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
