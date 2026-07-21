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
  Settings,
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
  useSetChannelThreadFollowed,
  useSetChannelTyping,
  useComposerDraftStore,
  useLastSelectedChannelStore,
  isImmutableSystemChannel,
  type ComposerDraftKey,
} from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { dmKeys, dmListOptions, useCreateOrFindDM } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { MobileListDetailLayout } from "../../common/mobile-list-detail-layout";
import { ContentEditor, type ContentEditorRef, type ContentEditorProps } from "../../editor/content-editor";
import { useNavigation } from "../../navigation/context";
import { avatarGlyph, avatarToneClass } from "../../common/initials";
import { useT } from "../../i18n/use-t";
import { useTimeAgo } from "../../i18n/use-time-ago";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { composePayloadKey } from "../hooks/use-compose-send-intent";
import { useComposerSend } from "../hooks/use-composer-send";
import {
  buildChatMessageParts,
  useComposerPendingAttachments,
} from "../hooks/use-composer-pending-attachments";
import { useEntryReadCursor } from "../hooks/use-entry-read-cursor";
import { useEntryAnchor } from "../hooks/use-entry-around-seq";
import { isChannelNameTakenError } from "../channel-create-error";
import { ChannelMessageList } from "./channel-message-list";
import { ChannelFilesPanel } from "./channel-files-panel";
import { ChannelStatsPanel } from "./channel-stats-panel";
import { ChannelProjectSettingsPanel } from "./channel-project-settings-panel";
import { ChannelSettingsSidePanel } from "./channel-settings-side-panel";
import { ChannelTasksBoard } from "./channel-tasks-board";
import { ChannelGroupAvatar } from "./channel-group-avatar";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { ThreadPanel } from "./thread-panel";
import { ComposerAttachmentTray } from "./composer-attachment-tray";
import { ComposerQuotePreview } from "./message-quote";
import type { QuoteTarget } from "./message-quote-types";
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
import { AgentPanelProvider } from "../../common/agent-panel-context";

export interface TypingActor {
  key: string;
  channelId: string;
  actorName: string;
  actorType: ChannelTypingPayload["actor_type"];
  expiresAt: number;
}

const EMPTY_TYPING_ACTORS: TypingActor[] = [];
const EMPTY_ACTIVE_TASKS: ChannelActiveTask[] = [];
const STOPPING_ALL_TASKS_ID = "__all__";
const identitySearchOptions = { extendedMatch: matchesPinyin };

// Slack-style presence: up to 4 faces + member-count badge. Opens the
// members panel (browse). Invite is a separate text button — no hollow "+".
function MemberPresenceStack({
  members,
  max = 4,
  size = 26,
}: {
  members: ChannelMemberBrief[];
  max?: number;
  size?: number;
}) {
  const visible = members.slice(0, max);
  const overlap = Math.round(size * 0.28);
  return (
    <span className="inline-flex items-center">
      {visible.map((m, i) => {
        const name = resolveActorDisplayName(
          m,
          m.member_type === "agent" ? "Agent" : "Member",
        );
        const seed = `${m.member_type}:${m.member_id}:${name}`;
        return (
          <span
            key={`${m.member_type}:${m.member_id}`}
            style={{ marginLeft: i === 0 ? 0 : -overlap }}
            className="inline-flex rounded-full ring-2 ring-background"
          >
            <ActorAvatar
              name={name}
              initials={avatarGlyph(name || "?")}
              avatarUrl={resolvePublicFileUrl(m.avatar_url)}
              isAgent={m.member_type === "agent"}
              size={size}
              className={avatarToneClass(seed)}
            />
          </span>
        );
      })}
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
  onStopAllTasks,
}: {
  typingActors?: TypingActor[];
  tasks?: ChannelActiveTask[];
  stoppingTaskId?: string | null;
  onStopTask?: (task: ChannelActiveTask) => void;
  onStopAllTasks?: (tasks: ChannelActiveTask[]) => void;
}) {
  const { t } = useT("channels");
  const [expanded, setExpanded] = useState(false);
  const typingNames = useMemo(
    () => typingActors.flatMap((a) => {
      const name = a.actorName.trim();
      return name ? [name] : [];
    }),
    [typingActors],
  );
  // The strip is the "in progress" control surface ONLY — who is running now +
  // Stop. Terminal outcomes (#388 no_reply / failed) stay visible as Activity
  // fact rows ("what happened"); the strip is not a history review surface. The
  // Retry action is removed per product decision (Frank 2026-07-14) — not a
  // pending follow-up: to have an agent try again you re-@ it, so a dedicated
  // Retry button isn't needed; failure remains visible as an Activity fact.
  // Terminal rows are excluded here — multiple agents used to stack the Stop
  // buttons horizontally (`overflow-x-auto`, no wrap) and garble; now they
  // collapse behind a single count + chevron.
  const stoppableTasks = useMemo(() => {
    const next: ChannelActiveTask[] = [];
    for (const task of tasks) {
      if (isTerminalChannelActiveTask(task)) continue;
      next.push(task);
    }
    return next;
  }, [tasks]);
  const typingLabel =
    typingNames.length === 0
      ? null
      : typingNames.length === 1
        ? t(($) => $.typing.single, { name: typingNames[0]! })
        : typingNames.length === 2
          ? t(($) => $.typing.pair, { a: typingNames[0]!, b: typingNames[1]! })
          : t(($) => $.typing.overflow, { a: typingNames[0]!, b: typingNames[1]!, count: typingNames.length });

  if (!typingLabel && stoppableTasks.length === 0) return null;

  return (
    <div
      className="flex min-h-6 flex-col gap-1 px-5 pb-2 text-xs text-muted-foreground"
      aria-live="polite"
      data-testid="conversation-activity-strip"
    >
      {typingLabel ? (
        <span className="flex min-w-0 items-center gap-1 truncate">
          <span className="truncate">{typingLabel}</span>
          <TypingDots />
        </span>
      ) : null}
      {stoppableTasks.length === 1 ? (
        <div className="flex min-w-0 items-center justify-between gap-2">
          <span className="flex min-w-0 items-center gap-1.5 truncate">
            <UnicodeSpinner className="shrink-0 text-muted-foreground/60" />
            <span className="truncate">
              {t(($) => $.agent_status.processing_single, { name: stoppableTasks[0]!.agent_name })}
            </span>
          </span>
          {onStopTask ? (
            <StopTaskButton
              task={stoppableTasks[0]!}
              stoppingTaskId={stoppingTaskId}
              onStopTask={onStopTask}
              t={t}
            />
          ) : null}
        </div>
      ) : stoppableTasks.length > 1 ? (
        <div className="flex flex-col gap-1">
          {/* Multiple agents collapse to one running summary. Keep Stop all
              outside the disclosure button so one click can cancel the whole group. */}
          <div className="flex min-w-0 items-center justify-between gap-2">
            <button
              type="button"
              onClick={() => setExpanded((value) => !value)}
              aria-expanded={expanded}
              className="flex min-w-0 flex-1 items-center justify-between gap-2 text-left hover:text-foreground"
            >
              <span className="flex min-w-0 items-center gap-1.5 truncate">
                <UnicodeSpinner className="shrink-0 text-muted-foreground/60" />
                <span className="truncate">
                  {t(($) => $.agent_status.processing_count, { count: stoppableTasks.length })}
                </span>
              </span>
              <ChevronDown
                className={cn("size-3.5 shrink-0 transition-transform", expanded && "rotate-180")}
                aria-hidden="true"
              />
            </button>
            {onStopAllTasks ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-6 shrink-0 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
                disabled={stoppingTaskId === STOPPING_ALL_TASKS_ID}
                onClick={() => onStopAllTasks(stoppableTasks)}
                aria-label={t(($) => $.agent_status.stop_all_aria, { count: stoppableTasks.length })}
              >
                <Square className="size-2.5 fill-current" />
                {t(($) => $.agent_status.stop_all)}
              </Button>
            ) : null}
          </div>
          {expanded ? (
            <div className="flex flex-col gap-1 pl-5">
              {stoppableTasks.map((task) => (
                <div key={task.task_id} className="flex min-w-0 items-center justify-between gap-2">
                  <span className="min-w-0 flex-1 truncate">{task.agent_name}</span>
                  {onStopTask ? (
                    <StopTaskButton
                      task={task}
                      stoppingTaskId={stoppingTaskId}
                      onStopTask={onStopTask}
                      t={t}
                    />
                  ) : null}
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function StopTaskButton({
  task,
  stoppingTaskId,
  onStopTask,
  t,
}: {
  task: ChannelActiveTask;
  stoppingTaskId: string | null;
  onStopTask: (task: ChannelActiveTask) => void;
  t: ReturnType<typeof useT<"channels">>["t"];
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="h-6 shrink-0 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
      disabled={stoppingTaskId === task.task_id || stoppingTaskId === STOPPING_ALL_TASKS_ID}
      onClick={() => onStopTask(task)}
      aria-label={t(($) => $.agent_status.stop_aria, { name: task.agent_name })}
    >
      <Square className="size-2.5 fill-current" />
      {t(($) => $.agent_status.stop)}
    </Button>
  );
}

function isTerminalChannelActiveTask(task: ChannelActiveTask) {
  return typeof task.outcome === "string";
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

function mobileBaseRestoreSuppressionKey(workspaceId: string) {
  return `multica:channels:skip-base-restore:${workspaceId}`;
}

function shouldSkipMobileBaseRestore(workspaceId: string) {
  if (typeof window === "undefined") return false;
  try {
    return window.sessionStorage.getItem(mobileBaseRestoreSuppressionKey(workspaceId)) === "1";
  } catch {
    return false;
  }
}

function setMobileBaseRestoreSuppression(workspaceId: string, suppressed: boolean) {
  if (typeof window === "undefined") return;
  try {
    const key = mobileBaseRestoreSuppressionKey(workspaceId);
    if (suppressed) window.sessionStorage.setItem(key, "1");
    else window.sessionStorage.removeItem(key);
  } catch {
    // Storage may be unavailable in privacy-restricted browser contexts. The
    // in-component ref still preserves Back when the route does not remount.
  }
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
  // the action list (Members / Share / Stats / Files / Settings); picking one
  // swaps the Drawer body to that section. A header Popover can render
  // off-screen on a narrow viewport, so the drawer is the reliable container.
  // `null` = closed. `"settings"` is the #576 group-settings surface (currently
  // just the Project section) — full-width like every other mobile panel here,
  // per Iris's placement spec.
  const [mobilePanel, setMobilePanel] = useState<
    "menu" | "members" | "stats" | "files" | "settings" | null
  >(null);
  // A route transition can remount this page between `/channels/[id]` and the
  // base `/channels` route. Preserve the mobile Back intent long enough for
  // that destination mount, then clear it so a later reload still restores the
  // user's saved group.
  const [skipInitialBaseRestore] = useState(() => shouldSkipMobileBaseRestore(wsId));
  // Channel main-content view switch (#562): the channel area is a top-level
  // `Chat | Tasks` tab, same level as the message list. Tasks renders a
  // channel-scoped board full-width in the main content area. Reset to Chat
  // whenever the active channel changes so switching channels lands on chat.
  const [channelView, setChannelView] = useState<"chat" | "tasks">("chat");
  // Tracks the channel `channelView` currently belongs to, so a channel switch
  // resets the tab to Chat via the render-time guard below (not an effect).
  // A ref (not state): it's bookkeeping that gates the reset, never a render
  // input (same convention as `reconciledRouteIdRef` below). `active` changing
  // already re-renders, so the reset fires without state driving the render.
  const channelViewChannelIdRef = useRef<string>("");
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
  const reconciledBaseRestoreIdRef = useRef<string | null>(null);
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
  const [quoteState, setQuoteState] = useState<{
    channelId: string | null;
    target: QuoteTarget | null;
    threadRootId: string | null;
    threadTarget: QuoteTarget | null;
  }>({ channelId: null, target: null, threadRootId: null, threadTarget: null });
  const threadEditorRef = useRef<ContentEditorRef>(null);
  const focusThreadComposerOnOpenRef = useRef(false);
  // #645 (Iris) — a true discriminated union: the type itself makes
  // thread+agent+settings-simultaneously-true unrepresentable, instead of
  // 3 independent nullable/boolean fields that "happen to" stay mutually
  // exclusive only because every call site remembers to clear the other
  // two. `threadDraftEmpty` stays a separate piece of state — it's thread
  // draft metadata, not part of which panel is showing.
  const [sidePanel, setSidePanel] = useState<
    | { kind: "none" }
    | { kind: "thread"; message: ChannelMessage }
    | { kind: "agent"; agentId: string }
    | { kind: "channel-settings" }
  >({ kind: "none" });
  const [threadDraftEmpty, setThreadDraftEmpty] = useState(true);
  const openThreadRoot = sidePanel.kind === "thread" ? sidePanel.message : null;
  const selectedAgentPanelId = sidePanel.kind === "agent" ? sidePanel.agentId : null;
  const channelSettingsOpen = sidePanel.kind === "channel-settings";
  const setOpenThreadRoot = useCallback((next: ChannelMessage | null) => {
    setSidePanel(next ? { kind: "thread", message: next } : { kind: "none" });
  }, []);
  const setSelectedAgentPanelId = useCallback((next: string | null) => {
    setSidePanel(next ? { kind: "agent", agentId: next } : { kind: "none" });
  }, []);
  const setChannelSettingsOpen = useCallback((next: boolean) => {
    setSidePanel(next ? { kind: "channel-settings" } : { kind: "none" });
  }, []);
  const resetSidePanelState = useCallback(() => {
    setSidePanel({ kind: "none" });
    setThreadDraftEmpty(true);
  }, []);
  const [convSearchOpen, setConvSearchOpen] = useState(false);
  const [convSearchQuery, setConvSearchQuery] = useState("");
  const [convSearchResults, setConvSearchResults] = useState<ChannelMessageSearchResult[]>([]);
  const [convSearchTotal, setConvSearchTotal] = useState(0);
  const [convSearchIndex, setConvSearchIndex] = useState(0);
  const [viewportReady, setViewportReady] = useState(false);
  const previousMobileRef = useRef<boolean | null>(null);
  // Mobile's Back button intentionally returns to the list. Keep that local
  // navigation decision until a reload or explicit conversation selection;
  // otherwise base-route restoration would immediately reopen the channel.
  const suppressBaseRouteRestoreRef = useRef(false);

  const {
    data: channels = [],
    isLoading,
    isSuccess: channelsLoaded,
  } = useQuery(channelsOptions(wsId));
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
  const lastSelectedChannelId = useLastSelectedChannelStore(
    (state) => state.lastSelectedChannelId,
  );
  const setLastSelectedChannelId = useLastSelectedChannelStore(
    (state) => state.setLastSelectedChannelId,
  );
  const clearLastSelectedChannelId = useLastSelectedChannelStore(
    (state) => state.clearLastSelectedChannelId,
  );

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

  // Existing canonical navigation reacts to external URL/list readiness, not a user event.
  // react-doctor-disable-next-line react-doctor/no-event-handler
  const hasRouteSelection = Boolean(channelId || activeDmId);
  const restoredBaseChannelId =
    !hasRouteSelection &&
    // Same render-time canonical-navigation reaction as above (#588 marker), kept
    // adjacent to its own line so react-doctor's suppression check passes.
    // react-doctor-disable-next-line react-doctor/no-event-handler
    !skipInitialBaseRestore &&
    !suppressBaseRouteRestoreRef.current &&
    channelsLoaded &&
    lastSelectedChannelId &&
    channels.some((channel) => channel.id === lastSelectedChannelId)
      ? lastSelectedChannelId
      : null;
  if (restoredBaseChannelId !== reconciledBaseRestoreIdRef.current) {
    if (restoredBaseChannelId) setActiveId(restoredBaseChannelId);
    reconciledBaseRestoreIdRef.current = restoredBaseChannelId;
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
    // #642 — same priority as the auto-select effects: prefer #general over
    // an arbitrary first channel for the render-time fallback too, so the
    // very first paint (before those effects commit) doesn't briefly show
    // the wrong channel.
    return isMobile
      ? explicit
      : (explicit ?? channels.find(isImmutableSystemChannel) ?? channels[0] ?? null);
  }, [channels, archivedChannels, activeId, activeDmId, isMobile]);
  const selectedAgentPanel = useMemo(
    () => (selectedAgentPanelId ? agents.find((agent) => agent.id === selectedAgentPanelId) ?? null : null),
    [agents, selectedAgentPanelId],
  );
  const isActiveArchived = !!active?.archived_at;
  // #642 — the workspace's system #general channel: immutable, auto-managed
  // roster (all human members + active workspace-visible agents, synced
  // server-side), no Settings entry, no invite/remove/archive affordance.
  const isActiveSystemChannel = active ? isImmutableSystemChannel(active) : false;
  const activeDraftKey = active ? (`channel:${active.id}` as const) : null;
  const activeDraft = activeDraftKey ? (composerDrafts[activeDraftKey]?.content ?? "") : "";
  const activeDraftEmpty = !activeDraft.trim();
  const quoteChannelId = active?.id ?? null;
  const quoteThreadRootId = openThreadRoot?.id ?? null;
  if (quoteState.channelId !== quoteChannelId || quoteState.threadRootId !== quoteThreadRootId) {
    setQuoteState({
      channelId: quoteChannelId,
      target: null,
      threadRootId: quoteThreadRootId,
      threadTarget: null,
    });
  }
  const quoteTarget = quoteState.channelId === quoteChannelId ? quoteState.target : null;
  const threadQuoteTarget = quoteState.threadRootId === quoteThreadRootId ? quoteState.threadTarget : null;
  const setQuoteTarget = useCallback((target: QuoteTarget | null) => {
    setQuoteState((current) => ({ ...current, target }));
  }, []);
  const setThreadQuoteTarget = useCallback((target: QuoteTarget | null) => {
    setQuoteState((current) => ({ ...current, threadTarget: target }));
  }, []);
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
  // Land on the conversation whenever the active channel changes; the Tasks tab
  // is a per-channel view, not a sticky global mode. Reset during render (the
  // React "adjust state on prop change" pattern used elsewhere in this file for
  // quoteState) rather than an effect, so there's no extra render / stale frame.
  if (channelViewChannelIdRef.current !== activeChannelId) {
    channelViewChannelIdRef.current = activeChannelId;
    setChannelView("chat");
  }
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
  const threadSurfaceRoot = useMemo(
    () => threadPage?.messages.find((message) => message.id === threadRoot?.id) ?? threadRoot,
    [threadPage?.messages, threadRoot],
  );
  const threadReplies = useMemo(
    () => {
      const messages = threadPage?.messages ?? [];
      return threadRoot ? messages.filter((msg) => msg.id !== threadRoot.id) : messages;
    },
    [threadPage?.messages, threadRoot],
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
  const setThreadFollowed = useSetChannelThreadFollowed();
  const setTyping = useSetChannelTyping();
  const handleThreadFollowChange = useCallback((followed: boolean) => {
    if (!threadSurfaceRoot) return;
    setThreadFollowed.mutate(
      {
        channelId: threadSurfaceRoot.channel_id,
        messageId: threadSurfaceRoot.id,
        followed,
      },
      {
        onError: () => toast.error(
          t(($) => followed ? $.thread.follow_failed : $.thread.unfollow_failed),
        ),
      },
    );
  }, [setThreadFollowed, t, threadSurfaceRoot]);
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
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);

  const uploadForActiveChannel = useCallback(
    async (file: File) => {
      if (!active) return null;
      return uploadWithToast(file, { channelId: active.id });
    },
    [active, uploadWithToast],
  );

  const channelPending = useComposerPendingAttachments({
    upload: uploadForActiveChannel,
    resetKey: active?.id ?? null,
  });
  const threadPending = useComposerPendingAttachments({
    upload: uploadForActiveChannel,
    resetKey: openThreadRoot?.id
      ? `${active?.id ?? ""}:${openThreadRoot.id}`
      : (active?.id ?? null),
  });

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
      avatarUrl?: string | null;
      presentation: ActorIdentityPresentation;
    }> = [
      ...availableMembers.map((m) => ({
        key: `user:${m.user_id}`,
        type: "user" as const,
        id: m.user_id,
        avatarUrl: m.avatar_url,
        presentation: resolveActorIdentityPresentation(m, m.email),
      })),
      ...availableAgents.map((a) => ({
        key: `agent:${a.id}`,
        type: "agent" as const,
        id: a.id,
        avatarUrl: a.avatar_url,
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
  // Channel-member agents to surface in the @ picker even when they aren't in
  // the member's personal agent list (e.g. a teammate's private Wendy). Channel
  // membership — not assignability — authorizes the mention.
  const channelAgentCandidates = useMemo<ContentEditorProps["scopedMentionAgents"]>(
    () => {
      const out: Array<{ id: string; name: string; display_name?: string | null }> = [];
      for (const m of channelMembers) {
        if (m.member_type === "agent") {
          out.push({ id: m.member_id, name: m.name, display_name: m.display_name });
        }
      }
      return out;
    },
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
  //
  // #642 — the workspace's system #general channel sorts first in the
  // unpinned list ("fixed first"). If the user pins it, it moves into
  // PINNED like any other pinned channel instead — `unpinnedChannels`
  // already excludes pinned entries, so there's no risk of it appearing
  // twice.
  const unpinnedChannels = useMemo(() => {
    const rest = channels.filter((c) => !c.pinned_at);
    const generalIndex = rest.findIndex(isImmutableSystemChannel);
    if (generalIndex <= 0) return rest;
    const general = rest[generalIndex]!;
    return [general, ...rest.slice(0, generalIndex), ...rest.slice(generalIndex + 1)];
  }, [channels]);
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
      // #642 — the system #general channel is immutable regardless of role;
      // this is the single capability gate archive AND (today, indirectly
      // via #576) project-editing both route through, so neither can drift.
      !isImmutableSystemChannel(channel) &&
      (channel.created_by === currentUserId ||
        currentUserRole === "owner" ||
        currentUserRole === "admin"),
    [currentUserId, currentUserRole],
  );
  // #576 blocker (Iris) — the group-settings Project picker must be gated by
  // the same creator/admin permission as archiving, plus archived-channel and
  // in-flight-mutation states: a plain member (or anyone viewing an archived
  // channel) could otherwise open the picker and have the mutation 403.
  const projectEditable = !!active && canArchive(active) && !isActiveArchived && !setChannelProject.isPending;
  const projectDisabledReason = !active
    ? undefined
    : isActiveArchived
      ? t(($) => $.settings.project_disabled_archived)
      : !canArchive(active)
        ? t(($) => $.settings.project_disabled_member)
        : undefined;
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

  // URL/list readiness is external, so direct-link persistence cannot move to a user event.
  useEffect(() => {
    // A direct group link is a real selection too. Remember it once list
    // membership confirms it, while leaving direct-message routes out of this
    // group-only preference.
    // react-doctor-disable-next-line react-doctor/no-event-handler
    if (channelId && channelsLoaded && channels.some((channel) => channel.id === channelId)) {
      setLastSelectedChannelId(channelId);
    }
  }, [channelId, channels, channelsLoaded, setLastSelectedChannelId]);

  useEffect(() => {
    // A remounted base route consumes the marker via its initial state. When
    // the component survives the route transition, the local ref above is the
    // guard, so clear the marker after the route has actually become base.
    if (skipInitialBaseRestore || (!channelId && suppressBaseRouteRestoreRef.current)) {
      setMobileBaseRestoreSuppression(wsId, false);
    }
  }, [channelId, skipInitialBaseRestore, wsId]);

  useEffect(() => {
    // A removed channel (or a workspace membership change) must not keep a
    // dead restore target around. The list is membership-filtered, so absence
    // here covers both deleted and no-longer-authorized channels.
    if (
      channelsLoaded &&
      lastSelectedChannelId &&
      !channels.some((channel) => channel.id === lastSelectedChannelId)
    ) {
      clearLastSelectedChannelId();
    }
  }, [channels, channelsLoaded, clearLastSelectedChannelId, lastSelectedChannelId]);

  useEffect(() => {
    previousMobileRef.current =
      typeof window !== "undefined" ? window.innerWidth < 768 : false;
    setViewportReady(true);
  }, []);

  // Single auto-select decision, computed once per relevant input change
  // rather than split across two effects that each independently watch
  // (and one of them writes) `activeId` — the original two-effect shape
  // triggered react-doctor's chained-state-update / event-in-effect
  // rules. Both prior cases are preserved as one branch each:
  //   - the live desktop→mobile viewport TRANSITION still restores a
  //     selection (matches the historical behavior, guarded by the
  //     previous/current mobile comparison);
  //   - the steady-state desktop case still auto-selects once viewport
  //     and channel data are known and nothing else claimed the slot.
  // Mobile is otherwise list-first — never auto-open a channel there, or
  // the list would never be reachable.
  //
  // `useIsMobile()` reports `false` on the very first render (its internal
  // state is still `undefined`) even on a phone, so we can't trust it here
  // on mount — measure the viewport directly (effects are client-only, so
  // `window` is always defined) to avoid auto-selecting before the
  // breakpoint is known.
  useEffect(() => {
    if (!viewportReady) return;
    // The previous/current mobile snapshot must update on EVERY run where
    // viewportReady, regardless of whether there's an active selection —
    // otherwise it goes stale while a channel is selected (Iris: desktop
    // active → resize to mobile → clear selection/back still on mobile —
    // without this the stale `previous === false` reads as "just
    // transitioned to mobile" and wrongly re-grabs #general, breaking
    // mobile list-first).
    const previous = previousMobileRef.current;
    const transitionedToMobile = previous === false && isMobile;
    previousMobileRef.current = isMobile;
    if (activeId || activeDmId) return;
    const onMobileViewport =
      isMobile || (typeof window !== "undefined" && window.innerWidth < 768);
    if (onMobileViewport && !transitionedToMobile) return;
    if (!channels[0]) return;
    // #642 — priority is deep-link > remembered > system #general > first
    // channel. Deep-link and remembered both already set activeId before
    // this effect runs (see the route-reconciliation and
    // restoredBaseChannelId effects above), so reaching here with
    // `!activeId` means neither fired — prefer #general over an arbitrary
    // first channel. This is externally-driven default resolution (reacts
    // to async channel-list load / viewport becoming known), not a chain
    // of derived state — there's no event handler to move it into.
    // react-doctor-disable-next-line react-doctor/no-chain-state-updates
    setActiveId((channels.find(isImmutableSystemChannel) ?? channels[0]).id);
  }, [viewportReady, isMobile, activeId, activeDmId, channels]);

  useEffect(() => {
    if (restoredBaseChannelId) {
      replace(wsPaths.channelDetail(restoredBaseChannelId));
    }
  }, [replace, restoredBaseChannelId, wsPaths]);

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

  // Clear search state when the active channel changes.
  useEffect(() => {
    setConvSearchOpen(false);
    setConvSearchQuery("");
    setConvSearchResults([]);
    setConvSearchTotal(0);
    setConvSearchIndex(0);
  }, [active?.id]);

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

  useEffect(() => {
    if (!activeChannelId || !threadRoot) return;
    markThreadRead({ channelId: activeChannelId, messageId: threadRoot.id });
  }, [activeChannelId, threadRoot, markThreadRead]);

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
      // Agent runtime stats are projected through the channel member list.
      // Refresh it with new agent replies so an already-open Agent panel can
      // show freshly persisted token stats without a full page reload.
      qc.invalidateQueries({ queryKey: channelKeys.members(e.channel_id) });
      if (e.channel_id === active?.id) markChannelRead(active.id);
    }
  });

  // The DM list also unions legacy chat_sessions, so a chat message updates a
  // DM row even though it isn't a channel event.
  useWSEvent("chat:message", () => {
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("task:cancelled", () => {
    if (!active?.id) return;
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(active.id) });
    qc.invalidateQueries({ queryKey: channelKeys.members(active.id) });
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });


  useWSEvent("task:completed", (payload) => {
    const event = payload as { chat_session_id?: string };
    if (!event.chat_session_id || !active?.id) return;
    qc.invalidateQueries({ queryKey: channelKeys.members(active.id) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("task:failed", (payload) => {
    const event = payload as { chat_session_id?: string };
    if (!event.chat_session_id || !active?.id) return;
    qc.invalidateQueries({ queryKey: channelKeys.members(active.id) });
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
    suppressBaseRouteRestoreRef.current = false;
    setActiveDmId(null);
    setActiveId(id);
    setLastSelectedChannelId(id);
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
    suppressBaseRouteRestoreRef.current = true;
    setMobileBaseRestoreSuppression(wsId, true);
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

  const handleStopAllChannelTasks = useCallback(async (tasks: ChannelActiveTask[]) => {
    if (!active?.id || tasks.length === 0) return;
    setStoppingChannelTaskId(STOPPING_ALL_TASKS_ID);
    const results = await Promise.allSettled(tasks.map((task) => api.cancelTaskById(task.task_id)));
    const stopped = results.filter((result) => result.status === "fulfilled").length;
    if (stopped === tasks.length) {
      toast.success(t(($) => $.agent_status.stop_all_success, { count: stopped }));
    } else if (stopped > 0) {
      toast.warning(t(($) => $.agent_status.stop_all_partial, { stopped, total: tasks.length }));
    } else {
      toast.error(t(($) => $.agent_status.stop_failed));
    }
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(active.id) });
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    setStoppingChannelTaskId(null);
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

  const handlePickFiles = (files: FileList | null) => {
    if (!files?.length) return;
    channelPending.addFiles(Array.from(files));
  };

  const handlePickThreadFiles = (files: FileList | null) => {
    if (!files?.length) return;
    threadPending.addFiles(Array.from(files));
  };

  const handleSend = () => {
    // Empty-payload early-return runs BEFORE the in-flight guard: after a send
    // succeeds, onSuccess clears the editor/tray and onSettled releases the guard —
    // a still-held Enter in that gap grabs empty content and stops here.
    if (!active) return;
    // Block while tray uploads are in flight so we never send without ready ids.
    if (channelPending.hasUploading) return;
    const content = editorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(content, channelPending.readyAttachmentParts);
    if (parts.length === 0) return;
    const attachmentIds = channelPending.readyAttachmentParts.map((p) => p.attachment_id);
    // Send lock (N held/auto-repeat Enter → 1 request) + payload-bound
    // client_message_id + the 3-way outcome, all owned by useComposerSend.
    const dispatched = channelSend.send({
      payloadKey: composePayloadKey(content, attachmentIds, quoteTarget?.id ?? ""),
      buildVars: (clientMessageId) => ({
        channelId: active.id,
        content,
        parts,
        quoteMessageId: quoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendMessage.mutate,
      onCommitted: () => {
        editorRef.current?.clearContent();
        channelPending.clear();
        setQuoteTarget(null);
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
    if (!active || !threadRoot) return;
    if (threadPending.hasUploading) return;
    const content = threadEditorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(content, threadPending.readyAttachmentParts);
    if (parts.length === 0) return;
    const attachmentIds = threadPending.readyAttachmentParts.map((p) => p.attachment_id);
    threadSend.send({
      payloadKey: composePayloadKey(
        content,
        attachmentIds,
        `${threadRoot.id}:${threadQuoteTarget?.id ?? ""}`,
      ),
      buildVars: (clientMessageId) => ({
        channelId: active.id,
        messageId: threadRoot.id,
        content,
        parts,
        quoteMessageId: threadQuoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendThreadMessage.mutate,
      onCommitted: () => {
        threadEditorRef.current?.clearContent();
        threadPending.clear();
        setThreadQuoteTarget(null);
        setThreadDraftEmpty(true);
      },
      onVisibleError: () => toast.error(t(($) => $.thread.send_failed)),
    });
  };

  const handleOpenThread = (message: ChannelMessage) => {
    focusThreadComposerOnOpenRef.current = true;
    setSidePanel({ kind: "thread", message });
  };

  const handleOpenAgentPanel = (agentId: string) => {
    setSidePanel({ kind: "agent", agentId });
  };

  // #645 — toggles the same exclusive slot; opening it always wins over
  // thread/agent (mirrors handleOpenAgentPanel), closing just clears it.
  const toggleChannelSettings = () => {
    setSidePanel(channelSettingsOpen ? { kind: "none" } : { kind: "channel-settings" });
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
      {
        onSuccess: () => setSelectedInvites(new Set()),
        onError: () => toast.error(t(($) => $.members.invite_failed)),
      },
    );
  };

  // Member management body (invite / members tabs + search). Extracted so the
  // SAME markup renders in the desktop header Popover and the mobile overflow
  // Drawer — no logic or layout duplicated. Guarded on `active` so the member-
  // remove handler always has a channel id.
  const memberPanelBody = active ? (
    <>
      {/* #642 — the system #general channel's roster is auto-managed
          server-side (every workspace member + active workspace-visible
          agent); there's nothing to invite, so the tab switcher itself
          would be a dead affordance. Go straight to a read-only list. */}
      {!isActiveSystemChannel && (
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
      )}
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
      {memberTab === "invite" && !isActiveSystemChannel ? (
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
                  className={cn(
                    "flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent",
                    selectedInvites.has(c.key) && "bg-accent/60",
                  )}
                >
                  <Checkbox
                    checked={selectedInvites.has(c.key)}
                    onCheckedChange={() => toggleInvite(c.key)}
                  />
                  <ActorAvatar
                    name={c.presentation.displayName}
                    initials={avatarGlyph(c.presentation.displayName || "?")}
                    avatarUrl={resolvePublicFileUrl(c.avatarUrl)}
                    isAgent={c.type === "agent"}
                    size={32}
                    className={avatarToneClass(c.key)}
                  />
                  <ActorIdentityRow
                    displayName={c.presentation.displayName}
                    handle={c.presentation.handle}
                    showHandle
                    primaryClassName="truncate text-sm font-medium"
                  />
                </label>
              ))
            )}
          </div>
          <div className="flex items-center justify-between gap-2 border-t p-2">
            <span className="text-xs text-muted-foreground">
              {t(($) => $.members.invite_selected, { count: selectedInvites.size })}
            </span>
            <Button
              size="sm"
              onClick={inviteSelected}
              disabled={selectedInvites.size === 0 || addMembers.isPending}
            >
              {t(($) => $.members.invite_cta)}
            </Button>
          </div>
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
                    initials={avatarGlyph(presentation.displayName || "?")}
                    avatarUrl={resolvePublicFileUrl(m.avatar_url)}
                    isAgent={isAgent}
                    size={32}
                    className={avatarToneClass(`${m.member_type}:${m.member_id}`)}
                  />
                  <ActorIdentityRow
                    displayName={presentation.displayName}
                    handle={presentation.handle}
                    showHandle
                    primaryClassName="truncate text-sm font-medium"
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
                  {!isActiveSystemChannel && (
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
                  )}
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
    const isSystemChannel = isImmutableSystemChannel(channel);
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
        {/* #642 — the system #general channel's archive action doesn't just
            disable, it disappears entirely (no separator either): a
            disabled-with-permission-tooltip item would misleadingly imply an
            owner/admin *could* archive it. */}
        {!isSystemChannel && (
          <>
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
                <span
                  className={cn(
                    "flex min-w-0 items-center gap-1 truncate text-sm text-foreground",
                    // Slack-style: an unread (non-muted) channel reads as a BOLD
                    // name, replacing the old saturated count block (#3).
                    realUnread > 0 && !isMuted ? "font-semibold" : "font-medium",
                  )}
                >
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
                  mentionCount={channel.mention_unread_count ?? 0}
                  mentionLabel={t(($) => $.sidebar.mention_indicator)}
                  mentionTooltip={t(($) => $.sidebar.mention_tooltip, {
                    count: channel.mention_unread_count ?? 0,
                    unread: realUnread,
                  })}
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
              {!isSystemChannel && (
                <>
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
                </>
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
    active && threadSurfaceRoot ? (
      <ThreadPanel
        root={threadSurfaceRoot}
        replies={threadReplies}
        currentUserId={currentUserId}
        currentUserName={currentUserName ?? undefined}
        isMobile={isMobile}
        onBack={() => setOpenThreadRoot(null)}
        followed={threadSurfaceRoot.thread_followed === true}
        followDisabled={
          threadLoading ||
          (setThreadFollowed.isPending &&
            setThreadFollowed.variables?.messageId === threadSurfaceRoot.id)
        }
        onFollowChange={handleThreadFollowChange}
        onViewParent={() => {
          setHighlightMessageId(threadSurfaceRoot.id);
          if (isMobile) setOpenThreadRoot(null);
        }}
        loading={threadLoading}
        loadError={threadError}
        onRetry={() => refetchThread()}
        onReact={handleReactToMessage}
        onQuoteMessage={setThreadQuoteTarget}
        onOpenAgent={handleOpenAgentPanel}
        quoteTarget={threadQuoteTarget}
        onClearQuote={() => setThreadQuoteTarget(null)}
        editor={
          <ContentEditor
            key={`thread-editor:${threadSurfaceRoot.id}`}
            ref={threadEditorRef}
            // Bare URLs stay plain text in the composer (#531/#542).
            plainUrls
            placeholder={t(($) => $.thread.composer_placeholder)}
            onUpdate={handleThreadEditorUpdate}
            onSubmit={handleThreadSend}
            mediaMode="external"
            onExternalFiles={threadPending.addFiles}
            submitOnEnter
            showBubbleMenu={false}
            mentionAllowedActorIds={channelMemberIds}
            scopedMentionAgents={channelAgentCandidates}
          />
        }
        onSend={handleThreadSend}
        sendDisabled={
          (threadDraftEmpty && threadPending.readyAttachmentParts.length === 0) ||
          threadPending.hasUploading
        }
        sending={sendThreadMessage.isPending}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer tray slot; identity is not memo-sensitive
        composerTray={
          <ComposerAttachmentTray
            pending={threadPending.pending}
            onRemove={threadPending.remove}
            onRetry={threadPending.retry}
            isMobile={isMobile}
          />
        }
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
            onStopAllTasks={handleStopAllChannelTasks}
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
  // #645 — never renders for the system #general channel even if somehow
  // triggered (defense in depth alongside the header entry point being
  // hidden outright — see isActiveSystemChannel below).
  const settingsPanel =
    channelSettingsOpen && active && !isActiveSystemChannel ? (
      <ChannelSettingsSidePanel
        channel={active}
        members={channelMembers}
        wsId={wsId}
        projectId={channelProjectId || null}
        onChangeProject={(projectId) => setChannelProject.mutate(projectId)}
        projectEditable={projectEditable}
        projectDisabledReason={projectDisabledReason}
        onClose={() => setChannelSettingsOpen(false)}
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
                {/* Slack-style: faces + count → browse members; separate
                   「邀请」text button → invite tab. No hollow "+" circle. */}
                <div className="flex items-center gap-2">
                  <Popover
                    onOpenChange={(open) => {
                      if (open) setMemberTab("members");
                    }}
                  >
                    <PopoverTrigger
                      className="flex items-center gap-1.5 rounded-md px-1 py-0.5 transition-colors hover:bg-accent"
                      aria-label={t(($) => $.header.view_members_aria)}
                    >
                      <MemberPresenceStack members={channelMembers} />
                      <span
                        className="inline-flex h-[26px] min-w-[26px] items-center justify-center rounded-md border border-border bg-background px-2 text-xs font-semibold text-muted-foreground"
                        aria-label={t(($) => $.header.member_count_aria, {
                          count: channelMembers.length,
                        })}
                      >
                        {channelMembers.length}
                      </span>
                    </PopoverTrigger>
                    <PopoverContent align="end" className="w-[360px] p-0">
                      {memberPanelBody}
                    </PopoverContent>
                  </Popover>
                  {!isActiveSystemChannel && (
                    <Popover
                      onOpenChange={(open) => {
                        if (open) setMemberTab("invite");
                      }}
                    >
                      <PopoverTrigger
                        className="inline-flex h-[26px] items-center justify-center rounded-md bg-primary px-2.5 text-xs font-semibold text-primary-foreground transition-colors hover:bg-primary/90"
                        aria-label={t(($) => $.header.invite_members_aria)}
                      >
                        {t(($) => $.members.invite)}
                      </PopoverTrigger>
                      <PopoverContent align="end" className="w-[360px] p-0">
                        {memberPanelBody}
                      </PopoverContent>
                    </Popover>
                  )}
                </div>
                <div className="flex items-center gap-1">
                  {/* #562 — the old #476 "open global Issues filtered to this
                      channel" entry is removed: the channel-scoped Tasks tab is
                      now the single, non-global-filter entry to this channel's
                      tasks (no more setSourceChannel write-back). */}
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
                  {/* #576/#645 — group settings now opens in the docked
                      right-side panel (same exclusive slot as Thread/Agent),
                      not a Popover — "布局要收敛", not another one-off card.
                      #642 — the system #general channel has no settings to
                      show (no project binding, immutable), so the entry
                      point itself is gone, not a disabled/empty panel. */}
                  {!isActiveSystemChannel && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className={cn("size-8", channelSettingsOpen && "bg-accent text-foreground")}
                      aria-label={t(($) => $.header.settings_aria)}
                      aria-pressed={channelSettingsOpen}
                      onClick={toggleChannelSettings}
                    >
                      <Settings className="size-4" />
                    </Button>
                  )}
                </div>
              </>
            )}
          />
              {/* #562 — channel main-content tab switch: Chat (message list) and
                  Tasks (channel-scoped board), full-width in the main area. Uses
                  the shared Tabs primitive so tablist/tab/tabpanel ARIA roles and
                  arrow-key navigation come for free; extend by adding a sibling
                  TabsTrigger + TabsContent for a new view. */}
              <Tabs
                value={channelView}
                onValueChange={(value) => setChannelView(value as "chat" | "tasks")}
                className="flex flex-1 min-h-0 flex-col gap-0"
              >
                <div className="shrink-0 border-b border-border/40 px-4">
                  <TabsList variant="line" className="h-auto">
                    <TabsTrigger value="chat" className="flex-none px-3 py-2">
                      {t(($) => $.view_tabs.chat)}
                    </TabsTrigger>
                    <TabsTrigger value="tasks" className="flex-none px-3 py-2">
                      {t(($) => $.view_tabs.tasks)}
                    </TabsTrigger>
                  </TabsList>
                </div>
                <TabsContent value="tasks" className="flex flex-1 min-h-0 flex-col text-base">
                  <ChannelTasksBoard channelId={active.id} />
                </TabsContent>
                <TabsContent value="chat" className="flex flex-1 min-h-0 flex-col text-base">
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
                onQuoteMessage={isActiveArchived ? undefined : setQuoteTarget}
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
                    onStopAllTasks={handleStopAllChannelTasks}
                  />
                  <Composer
                    surface="channel"
                    sendLabel={t(($) => $.composer.send)}
                    sendDisabled={
                      (activeDraftEmpty && channelPending.readyAttachmentParts.length === 0) ||
                      channelPending.hasUploading
                    }
                    sending={sendMessage.isPending}
                    onSend={handleSend}
                    isMobile={isMobile}
                    prefix={quoteTarget ? (
                      <ComposerQuotePreview
                        quote={quoteTarget}
                        onCancel={() => setQuoteTarget(null)}
                        cancelLabel={t(($) => $.quote.cancel)}
                      />
                    ) : undefined}
                    // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer tray slot; identity is not memo-sensitive
                    tray={
                      <ComposerAttachmentTray
                        pending={channelPending.pending}
                        onRemove={channelPending.remove}
                        onRetry={channelPending.retry}
                        isMobile={isMobile}
                      />
                    }
                    editor={
                      <ContentEditor
                        key={active.id}
                        ref={editorRef}
                        // Chat composer: typed/loaded bare URLs stay plain text
                        // (#531/#542) — made clickable on the read side, not here.
                        plainUrls
                        defaultValue={activeDraft}
                        placeholder={t(($) => $.composer.placeholder)}
                        onUpdate={handleEditorUpdate}
                        debounceMs={0}
                        onSubmit={handleSend}
                        mediaMode="external"
                        onExternalFiles={channelPending.addFiles}
                        submitOnEnter
                        showBubbleMenu={false}
                        enableIssueReferences
                        mentionAllowedActorIds={channelMemberIds}
                        scopedMentionAgents={channelAgentCandidates}
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
                        {/* #576 — the composer's ProjectPickerButton moved to the
                            group settings surface (header Settings popover /
                            mobile Settings drawer panel). Binding a channel to a
                            project is a group-configuration decision, not a
                            per-message composer action. */}
                        {/* LRM-205 — remove the composer toolbar # (井号) button.
                            Frank: 「把composer的警号删除掉」(井号/警号同音). Issue
                            refs stay available by typing # in the editor
                            (enableIssueReferences). */}
                      </>
                    }
                  />
                </>
              )}
                </TabsContent>
              </Tabs>
            </>
          )}
        </main>
  );
  const detailPane = !isMobile ? (
    <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
      <ResizablePanel id="conversation" minSize="50%" className="flex min-h-0 flex-col">
        {channelConversationPane}
      </ResizablePanel>
      {threadPanel || agentPanel || settingsPanel ? (
        <>
          <ResizableHandle />
          <ResizablePanel
            id={threadPanel ? "thread" : agentPanel ? "agent-files" : "channel-settings"}
            defaultSize={440}
            minSize={360}
            maxSize={640}
            groupResizeBehavior="preserve-pixel-size"
            className="border-l border-border/30 bg-background"
          >
            {threadPanel ?? agentPanel ?? settingsPanel}
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
    <AgentPanelProvider onOpenAgent={handleOpenAgentPanel}>
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
                      : mobilePanel === "settings"
                        ? t(($) => $.settings.title)
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
                  {/* #642 follow-up — same read-only-roster honesty as the
                      desktop trigger: no "Manage" wording for #general. */}
                  <span className="flex-1">
                    {t(($) => (isActiveSystemChannel ? $.header.view_members_aria : $.header.manage_members_aria))}
                  </span>
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
                {/* #642 — no settings row for the system #general channel;
                    same reasoning as the desktop Popover trigger. */}
                {!isActiveSystemChannel && (
                  <button
                    type="button"
                    onClick={() => setMobilePanel("settings")}
                    className="flex min-h-[44px] items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent"
                  >
                    <Settings className="size-5 shrink-0 text-muted-foreground" />
                    <span className="flex-1">{t(($) => $.settings.title)}</span>
                    <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                  </button>
                )}
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
            {mobilePanel === "settings" && (
              <div className="p-4">
                <ChannelProjectSettingsPanel
                  wsId={wsId}
                  projectId={channelProjectId || null}
                  onChange={(projectId) => setChannelProject.mutate(projectId)}
                  disabled={!projectEditable}
                  disabledReason={projectDisabledReason}
                />
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
    </AgentPanelProvider>
  );
}
