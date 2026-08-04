"use client";

import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Archive,
  ArchiveRestore,
  ArrowLeft,
  Bell,
  BellOff,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Mail,
  MessageCircle,
  MoreHorizontal,
  Paperclip,
  Pin,
  PinOff,
  Plus,
  Search,
  Smartphone,
  Trash2,
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
  enrichChannelMessagesPreservingAvatars,
  useEnsureMessageLoaded,
  channelsOptions,
  archivedChannelsOptions,
  channelMembersOptions,
  channelMemberManagementCapabilitiesOptions,
  invalidateChannelMemberRoster,
  channelInviteCandidatesOptions,
  channelProjectOptions,
  useSetChannelProject,
  useAddChannelMembers,
  useCreateChannel,
  useUpdateChannel,
  useDeleteChannel,
  useArchiveChannel,
  useRestoreChannel,
  useSetChannelPin,
  useMarkChannelRead,
  useMarkChannelUnread,
  useMuteChannel,
  useSetChannelNotifyPreference,
  useRemoveChannelMember,
  useUpdateChannelMemberRole,
  useTransferChannelOwnership,
  classifyRoleChangeFailure,
  type RoleChangeFailure,
  useSendChannelMessage,
  useSendChannelThreadMessage,
  useEditChannelMessage,
  useAddChannelReaction,
  useRemoveChannelReaction,
  useMarkChannelThreadRead,
  useSetChannelThreadFollowed,
  useSetChannelTyping,
  useComposerDraftStore,
  useLastSelectedChannelStore,
  isImmutableSystemChannel,
  channelMemberBadge,
  channelMemberRole,
  groupMemberActions,
  indexMemberManagementCapabilities,
  resolveGroupMemberActions,
  type GroupMemberActionKind,
  type ComposerDraftKey,
} from "@multica/core/channels";
import { useAuthStore } from "@multica/core/auth";
import { dmKeys, dmListOptions, useCreateOrFindDM } from "@multica/core/dm";
import type { DMItem } from "@multica/core/dm";
import { ApiError, api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWSEvent } from "@multica/core/realtime";
import { toast } from "sonner";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { projectListOptions } from "@multica/core/projects/queries";
import type {
  AgentPanelIdentitySnapshot,
  OpenAgentPanelFn,
} from "@multica/core/agents";
import {
  matchesActorIdentitySearch,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
} from "@multica/core/identity";
import type {
  Channel,
  ChannelActiveTask,
  ChannelNotifyLevel,
  ChannelInviteCandidate,
  ChannelMember,
  ChannelMessage,
  ChannelMessageSearchResult,
  ChannelTypingPayload,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
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
} from "@multica/ui/components/ui/drawer";
import { useIsMobile, useContainerNarrowerThan } from "@multica/ui/hooks/use-mobile";
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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { cn } from "@multica/ui/lib/utils";
import { SidebarTrigger, useSidebarSafe } from "@multica/ui/components/ui/sidebar";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { MobileListDetailLayout } from "../../common/mobile-list-detail-layout";
import {
  ContentEditor,
  type ContentEditorRef,
  type ContentEditorProps,
} from "../../editor/lazy-content-editor";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { ProjectPickerButton } from "../../common/project-picker-button";
import { PropRow } from "../../common/prop-row";
import { composePayloadKey } from "../hooks/use-compose-send-intent";
import { useComposerSend } from "../hooks/use-composer-send";
import {
  buildGroupMentionAllowedActorIds,
  inviteableUndeliveredMentions,
} from "../mention-scope";
import {
  ComposerSendErrorBar,
  type ComposerSendErrorState,
} from "./composer-send-error-bar";
import {
  ComposerPendingVoice,
  type PendingVoiceState,
} from "./composer-pending-voice";
import { voiceTargetId } from "./voice-target";
import {
  buildChatMessageParts,
  useComposerPendingAttachments,
} from "../hooks/use-composer-pending-attachments";
import { useComposerDraftHydrateSignal } from "../hooks/use-composer-draft-hydrate-signal";
import { useEntryReadCursor } from "../hooks/use-entry-read-cursor";
import { useEntryAnchor } from "../hooks/use-entry-around-seq";
import {
  buildRecordedVoiceMessageParts,
  type VoiceRecordingAttachment,
} from "../lib/voice-audio";
import { prepareVoicePlayback, voicePlaybackScope } from "../lib/voice-playback";
import {
  handleConvSearchInputKeyDown,
  orderConvSearchResultsNewestFirst,
} from "../lib/conv-search-navigation";
import { isChannelNameTakenError } from "../channel-create-error";
import { ChannelMessageList } from "./channel-message-list";
import type { ChannelDetailsTab } from "./channel-details-panel";
import { DeleteChannelDialog } from "./delete-channel-dialog";
import { RemoveMemberConfirmDialog } from "./remove-member-confirm-dialog";
import { ChannelNotifyPrefsDialog } from "./channel-notify-prefs";
import {
  channelNotifyLevelLabel,
  resolveChannelNotifyLevel,
} from "./channel-notify-level";
import { ChannelHashLandmark } from "./channel-hash-landmark";
import { ComposerAttachmentTray } from "./composer-attachment-tray";
import { ComposerQuotePreview } from "./message-quote";
import type { QuoteTarget } from "./message-quote-types";
import { useSelectionQuoteMenu } from "../lib/selection-quote-menu";
import {
  Composer,
  ConversationHeader,
  ReadOnlyConversationBanner,
} from "./conversation-surface";
import {
  isTerminalChannelActiveTask,
  listStoppableChannelTasks,
} from "./conversation-activity-tasks";
import { DmConversationRow, DmList, useDmRowActions } from "./dm-list";
import {
  dmAgentBubbleActivity,
  useAgentBubbleActivityByAgent,
} from "../../chat/lib/agent-bubble-unread";
import {
  ChannelListSkeleton,
  InitialChannelsShellSkeleton,
} from "./conversation-sidebar-list-skeleton";
import { type MentionPreviewResolver } from "./message-preview";
import {
  ConversationUnreadAffordance,
  isConversationMuted,
  MutedIndicator,
  sumUnmutedUnreadCounts,
} from "./conversation-muted";
import {
  CONVERSATION_SIDEBAR_ROW_ACTIVE,
  CONVERSATION_SIDEBAR_ROW_IDLE,
  CONVERSATION_SIDEBAR_UNREAD_BADGE,
} from "./conversation-sidebar-styles";
import { buildPinnedConversationEntries } from "./pinned-conversations";
import { PinnedConversationsSection } from "./pinned-conversations-section";
import { useSidebarSectionCollapsed } from "../hooks/use-sidebar-section-collapsed";
import { useJumpNotFoundToast } from "../hooks/use-jump-not-found-toast";
import { AgentPanelProvider } from "../../common/agent-panel-context";
import { MemberPanelProvider } from "../../common/member-panel-context";
import { MotionContent } from "../../common/motion-content";
import {
  CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY,
  useProfilePanelWidth,
} from "../../layout/use-profile-panel-width";
import { ChannelMembersList, type MemberRoleLabel } from "./channel-members-list";
import { memberFailureKey } from "./member-failure-key";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ChannelAddPeopleDialog } from "./channel-add-people-dialog";
import { StopAllAgentsDialog } from "./stop-all-agents-dialog";
import { ChannelPresenceCluster } from "./channel-agents-live-cue";

// LRM-1264 R3 — defer Tasks/Files/details/thread/agent/member graphs until
// those surfaces open. Base UI Tabs keep inactive panels mounted (hidden);
// children must also be conditional so hooks/queries do not retain on Chat.
const ChannelDetailsPanel = lazy(() =>
  import("./channel-details-panel").then((m) => ({ default: m.ChannelDetailsPanel })),
);
const ChannelTasksBoard = lazy(() =>
  import("./channel-tasks-board").then((m) => ({ default: m.ChannelTasksBoard })),
);
const ChannelFilesPanel = lazy(() =>
  import("./channel-files-panel").then((m) => ({ default: m.ChannelFilesPanel })),
);
const ThreadPanel = lazy(() =>
  import("./thread-panel").then((m) => ({ default: m.ThreadPanel })),
);
const ResolvedAgentSidePanel = lazy(() =>
  import("../../common/resolved-agent-side-panel").then((m) => ({
    default: m.ResolvedAgentSidePanel,
  })),
);
const MemberSidePanel = lazy(() =>
  import("../../members/member-side-panel").then((m) => ({
    default: m.MemberSidePanel,
  })),
);
const ChannelGoalCard = lazy(() =>
  import("./channel-goal-card").then((m) => ({ default: m.ChannelGoalCard })),
);
const DmConversation = lazy(() =>
  import("./dm-conversation").then((m) => ({ default: m.DmConversation })),
);

function ChannelLazyPanelFallback({ className }: { className?: string }) {
  return (
    <div className={className ?? "flex flex-1 min-h-0 flex-col gap-2 p-4"}>
      <Skeleton className="h-8 w-1/3" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  );
}

export interface TypingActor {
  key: string;
  channelId: string;
  actorName: string;
  actorType: ChannelTypingPayload["actor_type"];
  expiresAt: number;
}

const EMPTY_TYPING_ACTORS: TypingActor[] = [];
const STOPPING_ALL_TASKS_ID = "__all__";
const identitySearchOptions = { extendedMatch: matchesPinyin };

// #568 — below this, the two-pane desktop layout's conversation pane
// doesn't reliably have room for the direct action row (member cluster +
// invite + search) next to the truncating title: the row is all
// `shrink-0`, so it never yields space back to the title, and once the
// title has nothing left to give, the row overflows the header for real
// (`header.scrollWidth > header.clientWidth`).
//
// This is a CONTAINER width, not a viewport width — see
// `useContainerNarrowerThan` (packages/ui/hooks/use-mobile.ts). A global
// `useIsMobile`/`window.innerWidth` check is wrong here for two reasons
// that compound: (1) the list↔detail divider is user-draggable, so the
// conversation pane's width can diverge from the viewport in either
// direction; (2) docked side panels (`ChannelDetailsPanel` `variant=
// "panel"`, the thread panel, the agent-files panel) squeeze the SAME
// conversation pane further when they open, independent of both the
// viewport and the divider. Measuring the conversation `<main>`'s own
// rendered box (`detailHeaderContainerRef` below) reacts correctly to all
// three inputs at once, since each one ultimately shows up as a change to
// that element's own width.
//
// Value: live-measured (agent-browser, local dev server, real qa-bot group
// channel with the full row — member cluster + invite + search, ~216px
// natural width). Forced the conversation pane's rendered width through a
// binary search (bypassing the resizable-panel drag so the container width
// is controlled directly) to find the exact point where the direct row's
// `header.scrollWidth` first exceeds `clientWidth` with the title/meta
// collapsed all the way to zero (`min-w-0 truncate`, which has no minimum —
// it happily shrinks past legibility to nothing): that hard floor is
// exactly 256px, and it's independent of the channel's name/member-summary
// text length, since a fully collapsed `truncate` node renders at 0px
// regardless of its underlying string. Below 256px, no title width can
// prevent real overflow; 256px is therefore the highest breakpoint value
// that would ever be "too low" (guarantee a false negative). This
// breakpoint isn't set AT that floor, though — a channel name collapsed to
// nothing is unreadable — so it adds a 104px minimum title slot on top
// (enough for a handful of legible characters before ellipsis) for 360px
// total. This value is a fixed measurement of the row's own natural
// requirement — never the row's current rendered scrollWidth — so a
// narrow<->wide<->narrow container at/near 360px can't thrash: the same
// container width always yields the same decision.
const HEADER_ACTIONS_COMPACT_BREAKPOINT = 360;

// LRM-447 design A — up to 3 faces inside the Members chip (Invite lives in
// the Members dialog, not the header rail).
function EmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useT("channels");
  return (
    <div className="flex h-full items-center justify-center bg-background p-8">
      <div className="max-w-md rounded-3xl border border-border bg-card p-8 text-center shadow-sm">
        <div className="mx-auto flex size-12 items-center justify-center rounded-2xl bg-muted text-muted-foreground">
          <MessageCircle className="size-6" />
        </div>
        <h2 className="mt-5 text-xl font-semibold text-foreground">
          {t(($) => $.empty_state.title)}
        </h2>
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

// A selected DM that never resolves (see DM_RESOLVE_TIMEOUT_MS above) — an
// explicit "couldn't open, retry" state so this never reads as a blank page.
function DmOpenFailedState({ onRetry }: { onRetry: () => void }) {
  const { t } = useT("channels");
  return (
    <div className="flex min-h-0 min-w-0 flex-1 items-center justify-center bg-background">
      <button
        type="button"
        className="rounded-md px-3 py-2 text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={onRetry}
      >
        {t(($) => $.dm.open_failed)}
      </button>
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

export function ConversationActivityStrip({
  typingActors = EMPTY_TYPING_ACTORS,
}: {
  typingActors?: TypingActor[];
}) {
  const { t } = useT("channels");
  const typingNames = useMemo(
    () => typingActors.flatMap((a) => {
      const name = a.actorName.trim();
      return name ? [name] : [];
    }),
    [typingActors],
  );
  // The composer strip is typing-only. Working/failed/no_reply state belongs to
  // the inbox-backed header live cue so this component cannot fall back to the
  // old task/composer-strip path.
  const typingLabel =
    typingNames.length === 0
      ? null
      : typingNames.length === 1
        ? t(($) => $.typing.single, { name: typingNames[0]! })
        : typingNames.length === 2
          ? t(($) => $.typing.pair, { a: typingNames[0]!, b: typingNames[1]! })
          : t(($) => $.typing.overflow, { a: typingNames[0]!, b: typingNames[1]!, count: typingNames.length });

  if (!typingLabel) return null;

  return (
    <div
      className="flex min-h-6 flex-col gap-1 px-5 pb-2 text-xs text-muted-foreground"
      aria-live="polite"
      data-testid="conversation-activity-strip"
    >
      <span className="flex min-w-0 items-center gap-1 truncate">
        <span className="truncate">{typingLabel}</span>
        <TypingDots />
      </span>
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
  /**
   * Activity right-pane embed (LRM-388): hide the channel/DM list, do not own
   * `/channels` URL writes, and optionally pin the detail to thread-only or
   * channel-stream-only (desktop dual-pane is too wide for Activity).
   */
  embedded?: boolean;
  /** When `embedded`, force ThreadPanel-only or main timeline-only. */
  embeddedSurface?: "thread" | "channel";
  /**
   * Secondary "open in Channels" — Activity owns leaving the inbox route.
   * Required for embedded so View-in-channel never silently no-ops (LRM-238).
   */
  onOpenInChannels?: (opts: {
    channelId: string;
    messageId?: string;
    threadId?: string;
  }) => void;
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
export function ChannelsPage({
  channelId,
  embedded = false,
  embeddedSurface,
  onOpenInChannels,
  // react-doctor-disable-next-line react-doctor/prefer-useReducer
}: ChannelsPageProps = {}) {
  const { t } = useT("channels");
  const { t: tAgents } = useT("agents");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const { searchParams, replace, getShareableUrl, push } = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const currentUserName = useAuthStore((s) => s.user?.name ?? null);
  const { mutate: markChannelRead } = useMarkChannelRead();
  const isMobile = useIsMobile();
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_channels_layout",
  });
  // LRM-481 / LRM-400 — pixel side-dock width (not a lone ResizablePanelGroup).
  // Percentage PanelGroup + persisted 2-pane layout left a blank right half when
  // the dock was closed (Frank red-box). Flex row keeps the conversation mounted
  // full-width; drag still works when a dock is open.
  const {
    width: detailSideWidth,
    onResizePointerDown: onDetailSideResizePointerDown,
  } = useProfilePanelWidth(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY);
  // Embedded Activity pane never auto-picks a neighbor channel (LRM-238).
  const listFirstSelection = isMobile || embedded;
  // #568 — see `HEADER_ACTIONS_COMPACT_BREAKPOINT` above for the derivation.
  // `detailHeaderContainerRef` attaches to `channelConversationPane`'s
  // `<main>` below, so this reacts to viewport resize, the list↔detail
  // divider, AND a docked side panel opening/closing — all three change
  // that element's own rendered width.
  const [isHeaderActionsCompact, detailHeaderContainerRef] = useContainerNarrowerThan(
    HEADER_ACTIONS_COMPACT_BREAKPOINT,
  );
  // Mobile, and desktop-but-too-narrow (`isHeaderActionsCompact`): the
  // header's right-side actions collapse into a single "⋯" that opens the
  // LRM-494 Slack channel-details page (full-height Drawer), not a flat
  // bottom-sheet menu.
  const [mobilePanel, setMobilePanel] = useState<ChannelDetailsTab | null>(null);
  // #568 — the overflow Drawer only renders while `isMobile ||
  // isHeaderActionsCompact` (below). If the container widens past the
  // compact breakpoint while the Drawer is open, that condition alone
  // going false would unmount the whole `<Drawer>` out from under an
  // `open={true}` state — an unmount, not a controlled `open={false}` exit
  // transition — and `mobilePanel` would still hold its last value.
  // Re-narrowing later would then remount the Drawer already `open={true}`
  // (ghost reopen), with no user click. Clear the panel state declaratively
  // as soon as eligibility is lost, so the eligibility-loss path is an
  // unmount-with-state-already-cleared (Radix's own unmount cleanup runs,
  // not an open-state exit animation) and a later re-narrow starts from a
  // genuinely closed state.
  useEffect(() => {
    if (!isMobile && !isHeaderActionsCompact) setMobilePanel(null);
  }, [isMobile, isHeaderActionsCompact]);
  const [removeMemberTarget, setRemoveMemberTarget] = useState<ChannelMember | null>(null);
  // #832 — pending ownership transfer awaiting confirmation.
  const [transferTarget, setTransferTarget] = useState<ChannelMember | null>(null);
  // Retry must replay the action that failed, not re-derive one from current
  // state — the row's role may have changed underneath us.
  // Lazily initialised: `useRef(new Map())` would allocate a fresh Map on every
  // render and immediately discard it (react-doctor).
  const lastRoleActionRef = useRef<Map<string, "promote" | "demote" | "transfer"> | null>(null);
  lastRoleActionRef.current ??= new Map();
  // #838 — a recording whose upload succeeded but whose send failed. Kept per
  // surface (channel / thread) because each has its own composer; the toast is
  // the announcement, this is the durable record. Cleared ONLY by a committed
  // retry or an explicit delete — never a timer.
  // #838 H0 (Iris, 2nd pass) — keyed BY target, not "one record that knows its
  // target". A single slot still loses data: fail in A, switch to B, fail in B
  // → B overwrites A and A's recording is gone when the user returns. An unsent
  // recording may only disappear via a committed retry or an explicit delete,
  // so every target keeps its own entry.
  //
  // ⚠️ CURRENT PAGE SESSION ONLY — LOST ON REFRESH. Recovery across
  // reloads is task #849 (canonical, owned by Iris).
  // The contract says an unsent recording disappears only on a committed retry
  // or an explicit delete; a page refresh is a silent third way, which this
  // does NOT yet satisfy (Felix). It matters beyond a missing notice: the
  // attachment is already uploaded, so a refresh costs the user their only
  // route back to that recording and leaves an orphaned attachment server-side.
  // #849 covers the real fix (server-side author-owned pending voice draft,
  // projected back when the channel/thread is opened) — do not read this state
  // as durable across reloads.
  const [pendingVoices, setPendingVoices] = useState<Record<string, PendingVoiceState>>({});
  const rememberPendingVoice = useCallback((rec: PendingVoiceState) => {
    setPendingVoices((prev) => ({ ...prev, [rec.targetId]: rec }));
  }, []);
  const forgetPendingVoice = useCallback((targetId: string) => {
    setPendingVoices((prev) => {
      if (!(targetId in prev)) return prev;
      const next = { ...prev };
      delete next[targetId];
      return next;
    });
  }, []);
  // LRM-1356 — WHICH record has a retry actually in flight, keyed by the same
  // immutable target id as `pendingVoices`.
  //
  // This used to be read straight off `sendMessage.isPending` /
  // `sendThreadMessage.isPending`, but those are one mutation for the whole
  // surface and they outlive a channel/thread switch: any send in flight
  // anywhere on the page dimmed the unsent recording. Since both of its actions
  // guard on that flag (LRM-1354), it took Delete away too — and a committed
  // retry or an explicit delete are this record's ONLY two exits, so an
  // unrelated send left the user unable to resolve it at all. Scope the flag to
  // the record whose own retry was dispatched.
  const [retryingVoiceTargetId, setRetryingVoiceTargetId] = useState<string | null>(null);
  /**
   * Release the in-flight mark, but only if it is still THIS target's — a send
   * settling for another surface must not clear someone else's retry.
   */
  const settlePendingVoiceRetry = useCallback((targetId: string) => {
    setRetryingVoiceTargetId((current) => (current === targetId ? null : current));
  }, []);
  // #839 — durable in-row record of a failed removal, keyed by member identity.
  // The toast is the immediate announcement; it is NOT storage — it can be
  // dismissed (and expires on its own), and losing it must not erase the fact
  // that the removal failed (Iris). No timer: this clears only when the member
  // is actually gone (a successful retry drops the row with it) or when the user
  // explicitly dismisses the notice. Per member, so one failure cannot silently
  // replace another unresolved one.
  const [removeFailedKeys, setRemoveFailedKeys] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  // A route transition can remount this page between `/channels/[id]` and the
  // base `/channels` route. Preserve the mobile Back intent long enough for
  // that destination mount, then clear it so a later reload still restores the
  // user's saved group.
  const [skipInitialBaseRestore] = useState(() => shouldSkipMobileBaseRestore(wsId));
  // Channel main-content view switch (#562): the channel area is a top-level
  // `Chat | Issues | Files` tab (LRM-675 added Files), same level as the
  // message list. Tasks renders a channel-scoped board full-width in the main
  // content area. Reset to Chat whenever the active channel changes so
  // switching channels lands on chat.
  const [channelView, setChannelView] = useState<"chat" | "tasks" | "files">("chat");
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
  // ?message= deep-links to a specific message (e.g. from an overview mention
  // or a Reminder anchor). We scroll to and briefly highlight it, then clear
  // so it fades out.
  const [highlightMessageId, setHighlightMessageId] = useState<string | null>(
    () => searchParams.get("message"),
  );
  // ?thread=<rootId> deep-links straight into ThreadPanel (e.g. a Reminder
  // anchor whose target reply lives inside a thread, not on the main
  // timeline, or an Activity-page deep-link) — ?message= then names which
  // reply inside it to highlight, reusing the same highlightMessageId above
  // but routed to the thread surface instead of the main list (see
  // effectiveHighlightId below).
  const [threadDeepLinkId, setThreadDeepLinkId] = useState<string | null>(
    () => searchParams.get("thread"),
  );
  // AppLink navigation (e.g. clicking a Reminder anchor while already inside
  // Channels) is a same-pathname client-side push — it changes `searchParams`
  // without remounting this component, so a one-shot mount-time read alone
  // would miss it. `searchParams` itself is a NEW URLSearchParams instance
  // every render (see apps/web/platform/navigation.tsx), so depend on its
  // stable string form, not the object identity. These refs track which raw
  // URL value has already been applied/opened, independent of
  // highlightMessageId/threadDeepLinkId's own later mutations (flash-clear,
  // manually closing the thread) — comparing against those directly would
  // re-apply a still-present URL value right after it legitimately clears.
  const appliedMessageParamRef = useRef<string | null | undefined>(undefined);
  if (appliedMessageParamRef.current === undefined) {
    appliedMessageParamRef.current = searchParams.get("message");
  }
  const openedThreadDeepLinkRef = useRef<string | null>(null);
  // LRM-1063: drop mid-history around_seq cache once per deep-link target so
  // jump walks latest → older (infinite query has no newer direction).
  const deepLinkMessagesResetKeyRef = useRef<string | null>(null);
  const searchParamsString = searchParams.toString();
  const urlDeepLinkMessage = searchParams.get("message");
  const urlDeepLinkThread = searchParams.get("thread");
  const hasConversationDeepLink = !!(urlDeepLinkMessage || urlDeepLinkThread);
  useEffect(() => {
    const urlMessage = searchParams.get("message");
    if (urlMessage && urlMessage !== appliedMessageParamRef.current) {
      appliedMessageParamRef.current = urlMessage;
      setHighlightMessageId(urlMessage);
    }
    const urlThread = searchParams.get("thread");
    // react-doctor-disable-next-line react-doctor/no-event-handler -- syncing local state FROM an external system (the URL) on a genuine change, not faking a user-triggered event handler.
    if (urlThread !== threadDeepLinkId) setThreadDeepLinkId(urlThread);
    // react-doctor-disable-next-line react-doctor/exhaustive-deps -- intentionally keyed on the stable string form (searchParamsString), not searchParams/threadDeepLinkId directly; searchParams is a NEW object every render (see apps/web/platform/navigation.tsx) and threadDeepLinkId is read via a ref-equivalent comparison against the URL, not meant to re-run this effect on its own change.
  }, [searchParamsString]);
  const [search, setSearch] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  // LRM-655: remount-safe collapse (pairs with DmList / PINNED).
  const [channelsCollapsed, setChannelsCollapsed] = useSidebarSectionCollapsed(
    "channels",
    wsId,
  );
  const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null);
  const [archiveTarget, setArchiveTarget] = useState<Channel | null>(null);
  const [archivedOpen, setArchivedOpen] = useState(false);
  const editorRef = useRef<ContentEditorRef>(null);
  // LRM-695 — scopes the text-selection Quote/Copy mini-menu to the channel
  // message area (the scroller inside ChannelMessageList).
  const channelMessageAreaRef = useRef<HTMLDivElement>(null);
  const composerDrafts = useComposerDraftStore((s) => s.drafts);
  const storeSetComposerDraft = useComposerDraftStore((s) => s.setDraft);
  const storeClearComposerDraft = useComposerDraftStore((s) => s.clearDraft);
  const storeClearDraftContent = useComposerDraftStore((s) => s.clearDraftContent);
  const storeSetDraftAttachments = useComposerDraftStore((s) => s.setDraftAttachments);
  const [typingActors, setTypingActors] = useState<Record<string, TypingActor>>({});
  const [newName, setNewName] = useState("");
  const [newLarkChatId, setNewLarkChatId] = useState("");
  // #576 — optional project binding, set at creation time via the same
  // ProjectPickerButton the group-settings panel uses (channel-project-settings-panel.tsx).
  // `null` means unbound, which is the pre-existing default create behavior.
  const [newProjectId, setNewProjectId] = useState<string | null>(null);
  // Inline "name required" hint for the create popover. Empty names used to
  // silently default to "general", which collided with an existing general
  // channel and surfaced as an opaque failure (#216).
  const [createNameError, setCreateNameError] = useState(false);
  // Multi-select invite: keys are `${type}:${id}` so users and agents share one set.
  const [selectedInvites, setSelectedInvites] = useState<Set<string>>(new Set());
  const [addPeopleDialogOpen, setAddPeopleDialogOpen] = useState(false);
  const [membersQuery, setMembersQuery] = useState("");
  const [inviteQuery, setInviteQuery] = useState("");
  // LRM-623 — debounce filter only; input stays live. Same candidate pool /
  // API path — never re-fetch the full invite lists on each keystroke.
  const [debouncedInviteQuery, setDebouncedInviteQuery] = useState("");
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
  // #772 send-failure → composer-restore state (channel main + channel thread).
  const [channelSendError, setChannelSendError] =
    useState<ComposerSendErrorState | null>(null);
  const channelFailedContentRef = useRef<string>("");
  const [channelRestoreNonce, setChannelRestoreNonce] = useState(0);
  const [channelThreadSendError, setChannelThreadSendError] =
    useState<ComposerSendErrorState | null>(null);
  const channelThreadFailedContentRef = useRef<string>("");
  const [channelThreadRestoreNonce, setChannelThreadRestoreNonce] = useState(0);
  const [channelThreadRestoreText, setChannelThreadRestoreText] = useState("");
  const focusThreadComposerOnOpenRef = useRef(false);
  // #645 (Iris) — a true discriminated union: the type itself makes
  // thread+agent+details-simultaneously-true unrepresentable, instead of
  // 3 independent nullable/boolean fields that "happen to" stay mutually
  // exclusive only because every call site remembers to clear the other
  // two. `threadDraftEmpty` stays a separate piece of state — it's thread
  // draft metadata, not part of which panel is showing.
  // LRM-210 — channel details (About|Members|Files|Settings) owns the same
  // exclusive right-side slot the old channel-settings panel used.
  const [sidePanel, setSidePanel] = useState<
    | { kind: "none" }
    | { kind: "thread"; message: ChannelMessage }
    | {
        kind: "agent";
        agentId: string;
        snapshot?: AgentPanelIdentitySnapshot;
        /** LRM-877 Dock Stack — pop target human Profile under Agent. */
        returnToMemberId?: string;
        /** LRM-740 — Profile/Agent opened from Thread; close pops Thread back. */
        returnToThread?: ChannelMessage;
      }
    | {
        kind: "member";
        userId: string;
        /** LRM-740 — Profile opened from Thread; close pops Thread back. */
        returnToThread?: ChannelMessage;
      }
    | { kind: "channel-details"; tab: ChannelDetailsTab }
  >({ kind: "none" });
  const [threadDraftEmpty, setThreadDraftEmpty] = useState(true);
  const openThreadRoot = sidePanel.kind === "thread" ? sidePanel.message : null;
  const selectedAgentPanelId = sidePanel.kind === "agent" ? sidePanel.agentId : null;
  const selectedAgentPanelSnapshot =
    sidePanel.kind === "agent" ? (sidePanel.snapshot ?? null) : null;
  const selectedAgentReturnToMemberId =
    sidePanel.kind === "agent" ? (sidePanel.returnToMemberId ?? null) : null;
  const selectedMemberPanelId = sidePanel.kind === "member" ? sidePanel.userId : null;
  const channelDetailsOpen = sidePanel.kind === "channel-details";
  const channelDetailsTab =
    sidePanel.kind === "channel-details" ? sidePanel.tab : "about";
  const setOpenThreadRoot = useCallback((next: ChannelMessage | null) => {
    setSidePanel(next ? { kind: "thread", message: next } : { kind: "none" });
  }, []);
  // LRM-740 — X on Agent/Member restores Thread when the dock was pushed from
  // a Thread avatar click (embedded Activity thread-only surface would
  // otherwise fall through to a skeleton — silent no-op). Member pop stays on
  // `handlePopAgentToMember` / onBack — X dismisses the Agent frame entirely.
  const setSelectedAgentPanelId = useCallback((next: string | null) => {
    if (next) {
      setSidePanel({ kind: "agent", agentId: next });
      return;
    }
    setSidePanel((current) => {
      if (current.kind === "agent" && current.returnToThread) {
        return { kind: "thread", message: current.returnToThread };
      }
      return { kind: "none" };
    });
  }, []);
  const setSelectedMemberPanelId = useCallback((next: string | null) => {
    if (next) {
      setSidePanel({ kind: "member", userId: next });
      return;
    }
    setSidePanel((current) => {
      if (current.kind === "member" && current.returnToThread) {
        return { kind: "thread", message: current.returnToThread };
      }
      return { kind: "none" };
    });
  }, []);
  const openChannelDetails = useCallback(
    (tab: ChannelDetailsTab = "about") => {
      if (isMobile || isHeaderActionsCompact) {
        // LRM-494 — mobile/compact uses the full-page Drawer; drop any docked
        // details panel so home/settings rows aren't duplicated in the DOM.
        setSidePanel((current) =>
          current.kind === "channel-details" ? { kind: "none" } : current,
        );
        setMobilePanel(tab);
        return;
      }
      setSidePanel({ kind: "channel-details", tab });
    },
    [isMobile, isHeaderActionsCompact],
  );
  const closeChannelDetails = useCallback(() => {
    setSidePanel((current) =>
      current.kind === "channel-details" ? { kind: "none" } : current,
    );
    setMobilePanel(null);
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
    isPending: channelsPending,
    isSuccess: channelsLoaded,
  } = useQuery(channelsOptions(wsId));
  const { data: archivedChannels = [] } = useQuery(archivedChannelsOptions(wsId));
  const { data: workspaceMembers = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  // #576 — resolves the create-popover's selected project title, same query the
  // group-settings Project section (ChannelProjectSettingsPanel) uses. Keep it
  // dormant while the popover is closed: the project list is not rendered or
  // needed then, and ChannelsPage should not add a background request merely
  // because the optional create field exists.
  const { data: workspaceProjects = [] } = useQuery({
    ...projectListOptions(wsId),
    enabled: createOpen,
  });
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
  const { data: dms = [], refetch: refetchDms } = useQuery(dmListOptions(wsId));
  const bubbleActivityByAgent = useAgentBubbleActivityByAgent(wsId);
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
  // `embedded` counts as a route selection so Activity never restores a remembered
  // /channels id into the right pane (LRM-238 / LRM-388).
  // react-doctor-disable-next-line react-doctor/no-event-handler
  const hasRouteSelection = Boolean(channelId || activeDmId || embedded);
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
  // 2026-07-31 Wendy DM incident — a selected DM that never shows up in the list
  // (backend `GET /api/dm` gap, or any other never-resolving cause) used to
  // leave ConversationSwitchSkeleton spinning forever, which reads as a blank
  // page. After DM_RESOLVE_TIMEOUT_MS without resolving, swap to an explicit
  // failed state with a retry (refetches the DM list — cheap, since the list
  // query's staleTime is Infinity and would otherwise never retry on its own).
  const DM_RESOLVE_TIMEOUT_MS = 8000;
  const [dmResolveTimedOut, setDmResolveTimedOut] = useState(false);
  useEffect(() => {
    if (!activeDmId || activeDm) {
      setDmResolveTimedOut(false);
      return;
    }
    const timer = setTimeout(() => setDmResolveTimedOut(true), DM_RESOLVE_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, [activeDmId, activeDm]);
  // Desktop auto-selects the first channel only when nothing else is open —
  // never override an active DM selection. Mobile is list-first (no auto-open).
  // Embedded Activity (LRM-388) is also list-first: never fall back to #general
  // / first channel when the requested id is missing (no silent swap).
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
    return listFirstSelection
      ? explicit
      : (explicit ?? channels.find(isImmutableSystemChannel) ?? channels[0] ?? null);
  }, [channels, archivedChannels, activeId, activeDmId, listFirstSelection]);
  // LRM-622/623 — single invite-candidates fetch while Add people is open
  // (server omits existing channel members + archived agents only; every
  // non-archived WS agent is inviteable — no private/Wendy silent filter,
  // LRM-915 / #1613). Search stays on this in-memory pool with debounce —
  // no second full ListMembers+ListAgents.
  const inviteDiscoverChannelId =
    active?.kind === "group" && !active.archived_at ? active.id : null;
  const {
    data: inviteCandidateRows = [],
    isPending: inviteCandidatesPending,
    isError: inviteCandidatesError,
    refetch: refetchInviteCandidates,
  } = useQuery({
    ...channelInviteCandidatesOptions(inviteDiscoverChannelId ?? ""),
    enabled: !!inviteDiscoverChannelId && addPeopleDialogOpen,
  });
  const isActiveArchived = !!active?.archived_at;
  // #642 — the workspace's system #general channel: immutable, auto-managed
  // roster (all human members + active workspace-visible agents, synced
  // server-side), no Settings entry, no invite/remove/archive affordance.
  const isActiveSystemChannel = active ? isImmutableSystemChannel(active) : false;
  const activeDraftKey = active ? (`channel:${active.id}` as const) : null;
  const activeDraft = activeDraftKey ? (composerDrafts[activeDraftKey]?.content ?? "") : "";
  const channelDraftHydrateSignal = useComposerDraftHydrateSignal(activeDraftKey);
  const activeDraftEmpty = !activeDraft.trim();
  // #838 H0 — the record only exists for the surface it was recorded on. The
  // state outlives channel switches, so without this a failure in A would show
  // (and retry) in B. Compare against the IMMUTABLE target it was bound to.
  const channelPendingVoiceHere = active
    ? pendingVoices[voiceTargetId(active.id)] ?? null
    : null;
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
  // LRM-695 — text-selection Quote/Copy mini-menu over the channel message area
  // (desktop, fine pointer). Quote appends a `>` blockquote (author as plain
  // text, no @) to the channel composer via the editor markdown pipeline.
  const channelSelectionMenu = useSelectionQuoteMenu({
    containerRef: channelMessageAreaRef,
    onQuote: (md: string) => editorRef.current?.insertMarkdown(md),
  });
  const setConversationDraft = useCallback((key: ComposerDraftKey, value: string) => {
    if (!value.trim()) {
      // LRM-801 — deleting only the text must not wipe the image half of the
      // draft; full clears (send/manual) go through storeClearComposerDraft.
      storeClearDraftContent(key);
      return;
    }
    storeSetComposerDraft(key, value);
  }, [storeSetComposerDraft, storeClearDraftContent]);
  // LRM-1068: freeze entry unread count for the divider/pill label. Cold load
  // no longer uses around_seq — latest page + bottom settle (see useEntryAnchor).
  const entryAnchor = useEntryAnchor(
    active?.id,
    active?.last_read_seq,
    active?.real_unread_count ?? active?.unread_count,
  );
  // LRM-1063: notification / Activity / Reminder deep-links must not open on a
  // mid-history around_seq window — jump can only page older, so a tip target
  // ahead of last_read would false-exhaust as 「找不到消息」.
  const deepLinkAroundSeq = hasConversationDeepLink ? null : entryAnchor.aroundSeq;
  const {
    data: messagePages,
    isLoading: messagesLoading,
    isPending: messagesPending,
    isError: messagesError,
    refetch: refetchMessages,
    fetchNextPage: fetchOlderMessages,
    hasNextPage: hasOlderMessages,
    isFetchingNextPage: isFetchingOlderMessages,
  } = useInfiniteQuery(
    channelMessagesPageOptions(active?.id ?? "", {
      aroundSeq: deepLinkAroundSeq,
    }),
  );
  const activeChannelId = active?.id ?? "";
  useEffect(() => {
    const target = urlDeepLinkMessage || urlDeepLinkThread;
    if (!target || !activeChannelId) return;
    const key = `${activeChannelId}:${target}`;
    if (deepLinkMessagesResetKeyRef.current === key) return;
    deepLinkMessagesResetKeyRef.current = key;
    qc.removeQueries({ queryKey: channelKeys.messagesPage(activeChannelId) });
  }, [urlDeepLinkMessage, urlDeepLinkThread, activeChannelId, qc]);
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
  // #838 H0 — same rule for threads: bound to channel + thread root, so a
  // failure in thread A never surfaces (or retries) in thread B.
  const threadPendingVoiceHere =
    active && threadRoot
      ? pendingVoices[voiceTargetId(active.id, threadRoot.id)] ?? null
      : null;
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
      const filtered = threadRoot ? messages.filter((msg) => msg.id !== threadRoot.id) : messages;
      return enrichChannelMessagesPreservingAvatars(filtered);
    },
    [threadPage?.messages, threadRoot],
  );
  // Open ThreadPanel from ?thread=<rootId> once the route has resolved the
  // right channel. Only .id/.channel_id/.workspace_id of this stub matter —
  // threadRoot/threadSurfaceRoot above fall back to the real fetched message
  // (or a live one already in `messages`) the instant either resolves;
  // ThreadPanel itself renders its loading state (not this stub's placeholder
  // fields) while that's in flight, same as an ordinary click-to-open thread
  // whose full object hasn't round-tripped yet. Keyed on the VALUE, not a
  // fired-once boolean — a same-page AppLink navigation to a DIFFERENT
  // ?thread= must open the new one; closing the panel (URL unchanged) must
  // not re-open the same one.
  useEffect(() => {
    if (!threadDeepLinkId || !activeChannelId) return;
    if (openedThreadDeepLinkRef.current === threadDeepLinkId) return;
    openedThreadDeepLinkRef.current = threadDeepLinkId;
    // react-doctor-disable-next-line react-doctor/no-derived-state -- consumption of an external signal (?thread= URL param), deferred until activeChannelId resolves via async route reconciliation; not a value kept in sync with other state (the ref guard fires exactly once per distinct value, and the user can freely close/reopen sidePanel afterward independent of this).
    setOpenThreadRoot({
      id: threadDeepLinkId,
      channel_id: activeChannelId,
      workspace_id: wsId,
      seq: 0,
      type: "user",
      author_id: null,
      author_name: "",
      content: "",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      created_at: new Date(0).toISOString(),
    });
  }, [threadDeepLinkId, activeChannelId, wsId, setOpenThreadRoot]);
  // A thread-deep-linked highlight target is a REPLY, not a main-timeline
  // message — searching the main list for it would always miss (thread
  // replies aren't included there) and page through all of history for
  // nothing. Route the highlight to the thread surface instead (see the
  // <ThreadPanel highlightMessageId> below) and skip it here.
  const isThreadDeepLink = !!threadDeepLinkId;
  const { data: channelMembers = [], isPending: membersPending } = useQuery(channelMembersOptions(active?.id ?? ""));
  // LRM-872 / LRM-879 — server per-row can_remove (inviter + WS admin). Only
  // ordinary group channels expose the endpoint (system/DM → 404).
  const memberCapsEnabled =
    !!active?.id && active.kind === "group" && !isActiveSystemChannel && !isActiveArchived;
  const { data: memberManagementCapabilities } = useQuery(
    channelMemberManagementCapabilitiesOptions(active?.id ?? "", memberCapsEnabled),
  );
  const memberCapabilitiesByKey = useMemo(
    () => indexMemberManagementCapabilities(memberManagementCapabilities),
    [memberManagementCapabilities],
  );
  const { data: channelProjectId = "" } = useQuery(channelProjectOptions(wsId, active?.id ?? ""));
  const { data: activeTasks = [] } = useQuery(activeChannelTasksOptions(active?.id ?? ""));
  const [stoppingChannelTaskId, setStoppingChannelTaskId] = useState<string | null>(null);
  // LRM-405 — header Stop-all entry opens a confirm dialog; cancel/close
  // must not call cancel. Confirm reuses handleStopAllChannelTasks.
  const [stopAllConfirmOpen, setStopAllConfirmOpen] = useState(false);
  const stoppableChannelTasks = useMemo(
    () => listStoppableChannelTasks(activeTasks),
    [activeTasks],
  );
  // "Can send messages" on the channel surface today = not archived
  // (archived channels render a read-only banner and hide the composer).
  // No permission → hide the entry (explicit), never a silent no-op click.
  const canPostInChannel = !!active && !isActiveArchived;
  const hasStoppableChannelTasks = stoppableChannelTasks.length > 0;
  const isStoppingAllChannelTasks = stoppingChannelTaskId === STOPPING_ALL_TASKS_ID;
  const setChannelProject = useSetChannelProject(wsId, active?.id ?? "");
  const createChannel = useCreateChannel();
  const updateChannel = useUpdateChannel();
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
        onError: () => showErrorToast(
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
      { onError: () => showErrorToast(t(($) => $.message.edit_failed_toast)) },
    );
  }, [editChannelMessage, t]);
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
  // Use throwing `upload` (not uploadWithToast): the tray chip owns the error
  // UI. Swallowing into null produced the English-only "Upload failed" chip
  // with no API reason (LRM-426).
  const { upload } = useFileUpload(api);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const threadFileInputRef = useRef<HTMLInputElement | null>(null);
  // #576 — the mobile "..." Drawer's Settings tab (LRM-210's
  // `ChannelDetailsPanel`, `variant="page"`) hosts the project picker's own
  // dropdown (a Base UI Menu, portaled to `document.body` by default). The
  // Drawer is a modal Vaul/Radix Dialog that locks background interaction by
  // setting `body.style.pointerEvents = "none"` and re-enabling
  // `pointer-events: auto` only on its own content node — a menu portaled to
  // `document.body` is a DOM sibling of that content, not a descendant, so
  // it inherits the lock and becomes unclickable. Passing this ref down as
  // `ChannelDetailsPanel`'s `portalContainer` — which it attaches to its own
  // Settings-tab wrapper node inside `DrawerContent` and forwards to
  // `ChannelProjectSettingsPanel` — nests the picker's popup inside the
  // already-unlocked subtree. Left unset for the desktop docked
  // `ChannelDetailsPanel` (`variant="panel"`), which isn't inside a modal
  // Dialog, so the default `document.body` portal is fine there.
  const mobileSettingsDrawerBodyRef = useRef<HTMLDivElement | null>(null);

  const uploadForActiveChannel = useCallback(
    async (file: File) => {
      if (!active) {
        throw new Error("No active channel");
      }
      return upload(file, { channelId: active.id });
    },
    [active, upload],
  );

  const channelPending = useComposerPendingAttachments({
    upload: uploadForActiveChannel,
    resetKey: active?.id ?? null,
    // LRM-801 — tray rides the same per-channel draft as the text.
    persistence: activeDraftKey
      ? {
          load: () =>
            useComposerDraftStore.getState().drafts[activeDraftKey]?.attachments,
          save: (attachments) => storeSetDraftAttachments(activeDraftKey, attachments),
          hydrateSignal: channelDraftHydrateSignal,
        }
      : undefined,
  });
  const threadPending = useComposerPendingAttachments({
    upload: uploadForActiveChannel,
    resetKey: openThreadRoot?.id
      ? `${active?.id ?? ""}:${openThreadRoot.id}`
      : (active?.id ?? null),
  });

  // Flat candidate list (users + agents) for Add people; chips use the
  // unfiltered pool so selections survive search. Pool comes from LRM-622
  // invite-candidates (already excludes channel members).
  const allInviteCandidates = useMemo(() => {
    const mapRow = (c: ChannelInviteCandidate) => ({
      key: `${c.member_type}:${c.member_id}`,
      type: c.member_type,
      id: c.member_id,
      avatarUrl: c.avatar_url,
      presentation: resolveActorIdentityPresentation(
        { name: c.name, display_name: c.display_name },
        c.member_type === "agent" ? "Agent" : c.email?.trim() || "Member",
      ),
    });
    return inviteCandidateRows.map(mapRow);
  }, [inviteCandidateRows]);
  // Debounced filter over the same in-memory pool (no second full fetch).
  useEffect(() => {
    const handle = window.setTimeout(() => setDebouncedInviteQuery(inviteQuery), 200);
    return () => window.clearTimeout(handle);
  }, [inviteQuery]);
  const inviteCandidates = useMemo(() => {
    const q = debouncedInviteQuery.trim();
    return q
      ? allInviteCandidates.filter((c) =>
          matchesActorIdentitySearch(
            c.presentation.displayName,
            c.presentation.handle,
            q,
            identitySearchOptions,
          ),
        )
      : allInviteCandidates;
  }, [allInviteCandidates, debouncedInviteQuery]);
  const inviteCandidatesLoading = addPeopleDialogOpen && inviteCandidatesPending;
  const inviteCandidatesErrorFlag = addPeopleDialogOpen && inviteCandidatesError;
  const retryInviteCandidates = useCallback(() => {
    void refetchInviteCandidates();
  }, [refetchInviteCandidates]);
  const filteredMembers = useMemo(() => {
    const q = membersQuery.trim();
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
  }, [channelMembers, membersQuery, t]);
  // Channel-member id set (DM / narrow scope).
  const channelMemberIds = useMemo(
    () => new Set(channelMembers.map((m) => m.member_id)),
    [channelMembers],
  );
  // Group @ picker: workspace members + agents (Alice #1984 / Raft undelivered).
  // DM stays channel-member only (fail-closed).
  const mentionAllowedActorIds = useMemo(() => {
    if (!active || active.kind !== "group") return channelMemberIds;
    return buildGroupMentionAllowedActorIds({
      workspaceUserIds: workspaceMembers.map((m) => m.user_id),
      workspaceAgentIds: agents.map((a) => a.id),
      channelMemberIds: channelMembers.map((m) => m.member_id),
    });
  }, [active, agents, channelMemberIds, channelMembers, workspaceMembers]);
  // Channel-member agents + (on group) workspace agents so picker can inject
  // ids not already in the personal agent list.
  const channelAgentCandidates = useMemo<ContentEditorProps["scopedMentionAgents"]>(
    () => {
      const out: Array<{ id: string; name: string; display_name?: string | null }> = [];
      const seen = new Set<string>();
      for (const m of channelMembers) {
        if (m.member_type === "agent" && !seen.has(m.member_id)) {
          seen.add(m.member_id);
          out.push({ id: m.member_id, name: m.name, display_name: m.display_name });
        }
      }
      if (active?.kind === "group") {
        for (const a of agents) {
          if (a.id && !seen.has(a.id) && !a.archived_at) {
            seen.add(a.id);
            out.push({
              id: a.id,
              name: a.name,
              display_name: a.display_name,
            });
          }
        }
      }
      return out;
    },
    [active?.kind, agents, channelMembers],
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
      return { memberCount, agentCount };
    },
    [channelMembers],
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
  // LRM-239 / LRM-235 — permanent delete is stricter than archive: workspace
  // owner/admin only (channel creator who is a plain member cannot delete).
  // System channels never get a Settings tab, so this gate is defense-in-depth.
  const canDeleteChannel = useCallback(
    (channel: Channel) =>
      !isImmutableSystemChannel(channel) &&
      (currentUserRole === "owner" || currentUserRole === "admin"),
    [currentUserRole],
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
  const manageDisabledReason = !active
    ? undefined
    : isActiveArchived
      ? t(($) => $.settings.rename_disabled_archived)
      : !canArchive(active)
        ? t(($) => $.settings.rename_disabled_member)
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
    if (embedded) return;
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
    // react-doctor-disable-next-line react-doctor/no-chain-state-updates, react-doctor/no-adjust-state-on-prop-change
    setActiveId((channels.find(isImmutableSystemChannel) ?? channels[0]).id);
  }, [viewportReady, isMobile, activeId, activeDmId, channels, embedded]);

  useEffect(() => {
    // Activity embed must never rewrite the browser URL to /channels/... —
    // that jumps the user out of Activity into the Messages main column
    // (Frank / LRM-388 AC: 禁止跳进频道主列).
    if (embedded) return;
    if (restoredBaseChannelId) {
      replace(wsPaths.channelDetail(restoredBaseChannelId));
    }
  }, [embedded, replace, restoredBaseChannelId, wsPaths]);

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
  // While a ?thread= deep-link keeps the side panel open, ?message= names a
  // REPLY and must not drive the main timeline. Once the panel is closed
  // (e.g. LRM-389 open-in-main → view parent), the same highlightMessageId
  // can be the ROOT and belongs on the main list — do not keep nulling it
  // just because the URL still carries ?thread=.
  const effectiveHighlightId = convSearchOpen
    ? (convSearchResults[convSearchIndex]?.message_id ?? null)
    : isThreadDeepLink && openThreadRoot
      ? null
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
    // LRM-1063: channel id may still be reconciling (query disabled) or the
    // first page in flight — never toast/exhaust in that window.
    isPending: !activeChannelId || messagesPending,
  });
  // LRM-736 AC — toast + keep the inline notice (#835 durable record).
  useJumpNotFoundToast({
    missing: jumpStatus === "exhausted",
    targetId: effectiveHighlightId,
    message: t(($) => $.message_loading.jump_not_found),
  });
  // Thread deep-link: reply missing after the thread page settles (deleted).
  useJumpNotFoundToast({
    missing:
      isThreadDeepLink &&
      !!highlightMessageId &&
      !!openThreadRoot &&
      !threadLoading &&
      !threadError &&
      !!threadPage &&
      !threadPage.messages.some((m) => m.id === highlightMessageId),
    targetId: highlightMessageId,
    message: t(($) => $.message_loading.jump_not_found),
  });

  useEffect(() => {
    if (convSearchOpen || (isThreadDeepLink && openThreadRoot)) return;
    // Only flash-then-clear a quote highlight once its target is actually on
    // screen — while older pages are still being paged toward it, keep it set
    // so the scroll lands when it loads. The timer's cleanup keeps this a
    // reactive flash, not a state-driven event handler.
    if (!highlightMessageId || !jumpTargetLoaded) return;
    const clear = setTimeout(() => setHighlightMessageId(null), 2500);
    return () => clearTimeout(clear);
  }, [
    highlightMessageId,
    convSearchOpen,
    jumpTargetLoaded,
    isThreadDeepLink,
    openThreadRoot,
  ]);

  // Thread-panel counterpart of the clear-after-flash effect above — the
  // target here is a REPLY (threadReplies), never the main list, so it needs
  // its own "is it actually loaded/visible yet" check.
  useEffect(() => {
    if (!isThreadDeepLink || !openThreadRoot || !highlightMessageId) return;
    if (!threadReplies.some((m) => m.id === highlightMessageId)) return;
    const clear = setTimeout(() => setHighlightMessageId(null), 2500);
    return () => clearTimeout(clear);
  }, [isThreadDeepLink, openThreadRoot, highlightMessageId, threadReplies]);

  // Clear search state when the active channel changes (render-time adjust —
  // avoids a stale-search flash between the channel switch commit and an
  // effect). Ref tracks which channel the current search UI belongs to.
  const convSearchChannelIdRef = useRef<string | null>(null);
  const activeSearchChannelId = active?.id ?? null;
  if (convSearchChannelIdRef.current !== activeSearchChannelId) {
    convSearchChannelIdRef.current = activeSearchChannelId;
    setConvSearchOpen(false);
    setConvSearchQuery("");
    setConvSearchResults([]);
    setConvSearchTotal(0);
    setConvSearchIndex(0);
  }

  // Debounced in-conversation search. Empty query clears are handled where
  // the query is written (input onChange / channel switch) — not here — so
  // this effect only owns the async fetch.
  //
  // LRM-1296 — clearing the debounce timer cannot stop a request that already
  // left, so a superseded response used to land last and overwrite the newer
  // results (count, jump target, and index reset to 0) while the input already
  // showed the newer query; a request fired just before a channel switch could
  // likewise repopulate search state belonging to the previous channel. The
  // per-run `cancelled` flag drops any response whose run is no longer current
  // — query change, panel close, and channel switch all re-run this effect
  // (`active` is a dep). The DM surface already drops stale hits in its reducer
  // (`dm-conversation.tsx` `setSearchResults` compares `action.query`); this is
  // the group-channel equivalent.
  useEffect(() => {
    if (!convSearchOpen || !active) return;
    const q = convSearchQuery.trim();
    if (!q) return;
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const res = await api.searchChannelMessages(active.id, q);
        if (!cancelled) {
          // LRM-753 — newest-first so index 0 is「最近命中」; list scroll follows
          // effectiveHighlightId via ChannelMessageList.
          setConvSearchResults(orderConvSearchResultsNewestFirst(res.results));
          setConvSearchTotal(res.total);
          setConvSearchIndex(0);
        }
      } catch {
        if (!cancelled) showErrorToast(t(($) => $.conv_search.error));
      }
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
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

  // New messages (from others / agents) refresh the DM/task/member surfaces
  // and the open thread. Keep the active channel marked read while viewing
  // it. #689 perf audit: channelKeys.list(wsId) is NOT invalidated here —
  // useRealtimeSync's own central "channel:message" subscriber already does
  // that for every event; this second subscriber repeating it was a literal
  // duplicate invalidation on every single incoming message.
  useWSEvent("channel:message", (payload) => {
    const e = payload as ChannelMessage;
    // The DM list unions dm_channel items, so a channel message may change a DM
    // row's preview / unread — refresh it too.
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    if (e.channel_id) {
      qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(e.channel_id) });
      // Agent runtime stats are projected through the channel member list.
      // Refresh it with new agent replies so an already-open Agent panel can
      // show freshly persisted token stats without a full page reload.
      invalidateChannelMemberRoster(qc, e.channel_id);
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
    invalidateChannelMemberRoster(qc, active.id);
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });


  useWSEvent("task:completed", (payload) => {
    const event = payload as { chat_session_id?: string };
    if (!event.chat_session_id || !active?.id) return;
    invalidateChannelMemberRoster(qc, active.id);
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  useWSEvent("task:failed", (payload) => {
    const event = payload as { chat_session_id?: string };
    if (!event.chat_session_id || !active?.id) return;
    invalidateChannelMemberRoster(qc, active.id);
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
  });

  // Another client hard-deleted a channel — drop it from active + Archived.
  // If it was the open one, `active` falls back via the memo.
  useWSEvent("channel:deleted", (payload) => {
    const e = payload as { id?: string };
    // LRM-485 — drop both caches immediately; invalidate-only left ghosts.
    if (e.id) {
      const drop = <T extends { id: string }>(prev: T[] | undefined) =>
        prev ? prev.filter((c) => c.id !== e.id) : prev;
      qc.setQueryData(channelKeys.list(wsId), drop);
      qc.setQueryData(channelKeys.archivedList(wsId), drop);
    }
    qc.invalidateQueries({ queryKey: channelKeys.all(wsId) });
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
      {
        name,
        lark_chat_id: newLarkChatId.trim() || undefined,
        // Omit entirely (not null) when unset, so the request body doesn't
        // carry a `project_id` key at all — matching the pre-existing create
        // flow exactly when no project is picked.
        project_id: newProjectId ?? undefined,
      },
      {
        onSuccess: (channel: Channel) => {
          selectChannel(channel.id);
          // Beckham v2 §4 — pin the freshly-created group to the CREATOR's own
          // sidebar so it's easy to find right after creating it. Reuses the
          // existing per-user channel pin (the creator can unpin like any other),
          // and only pins for that one user — not a workspace-wide pin. Ordinary
          // groups only (the create dialog makes those; gate defensively). Pin is
          // best-effort: a failure never blocks or rolls back — the group is
          // created and selected regardless. But we don't swallow it silently:
          // the product promises "new group pinned", so a pin failure surfaces a
          // non-blocking info toast (not an error — creation succeeded) telling
          // the user they can pin manually, rather than leaving them with neither
          // the pin nor any signal.
          if (channel.kind === "group") {
            setChannelPin.mutate(
              { channelId: channel.id, pinned: true },
              { onError: () => toast.info(t(($) => $.sidebar.create_pin_failed)) },
            );
          }
          setNewName("");
          setNewLarkChatId("");
          setNewProjectId(null);
          setCreateNameError(false);
          setCreateOpen(false);
        },
        onError: (err) => {
          // Duplicate (workspace, name) comes back as a 409 with a stable code;
          // localise off the code, not the server's English string.
          showErrorToast(
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
  // Embedded Activity owns the URL (/inbox?channel=…); never replace to
  // /channels/[id] from an embed (LRM-388 — stay on the Activity right pane).
  const selectChannel = (id: string) => {
    resetSidePanelState();
    suppressBaseRouteRestoreRef.current = false;
    setActiveDmId(null);
    setActiveId(id);
    setLastSelectedChannelId(id);
    if (!embedded) replace(wsPaths.channelDetail(id));
  };

  // Select a DM (from the DIRECT MESSAGES region). Clears the group selection
  // and reflects the DM in the URL so it can be shared / deep-linked.
  const selectDm = (dm: DMItem) => {
    resetSidePanelState();
    setActiveId(null);
    setActiveDmId(dm.id);
    if (!embedded) replace(wsPaths.channelDetail(dm.id));
  };

  // "Send message" entry point on a channel member row: create-or-find the DM
  // with that member and open it in place (we're already on the Messages view,
  // so no navigation round-trip is needed — selectDm switches the detail pane).
  const openDmWithMember = (member: ChannelMember) => {
    createOrFindDm.mutate(
      { peer_type: member.member_type, peer_id: member.member_id },
      {
        onSuccess: (dm) => selectDm(dm),
        onError: () => showErrorToast(t(($) => $.dm.open_failed)),
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
    if (!embedded) replace(wsPaths.channels());
  };

  const handleDelete = () => {
    const target = deleteTarget;
    if (!target) return;
    deleteChannel.mutate(target.id, {
      onSuccess: () => {
        toast.success(t(($) => $.delete_dialog.toast_success));
        // If the open channel was the one removed, drop the selection so the
        // `active` memo falls back to the first remaining channel.
        if (target.id === activeId) {
          setActiveId(null);
          if (!embedded) replace(wsPaths.channels());
        }
        closeChannelDetails();
        setMobilePanel(null);
        setDeleteTarget(null);
      },
      // LRM-449 / LRM-238 — surface API reason (403/409/500 body.error); never
      // collapse every failure to a bare "Failed to delete channel".
      onError: (err) => {
        const reason =
          err instanceof ApiError && err.message.trim() ? err.message.trim() : null;
        showErrorToast(reason ?? t(($) => $.delete_dialog.toast_failed));
      },
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
      onError: () => showErrorToast(t(($) => $.archive_dialog.error)),
    });
  };

  const handleRestoreChannel = (channelId: string) => {
    restoreChannel.mutate(channelId, {
      onError: () => showErrorToast(t(($) => $.archive_dialog.restore_error)),
    });
  };

  const handleStopChannelTask = useCallback(async (
    task: ChannelActiveTask,
    // LRM-1350: Working-list resolved label (same cascade as live cue). Never
    // toast raw `task.agent_name` — that may be the Unknown Agent sentinel.
    displayName: string,
  ) => {
    if (!active?.id) return;
    // Terminal failed/no_reply rows are dismissed client-side in the live cue
    // (LRM-581) — cancel is only for in-flight wakes.
    if (isTerminalChannelActiveTask(task)) return;
    // LRM-425 / LRM-238 — authoritative id is inbox_event_id; never fall back
    // to /api/tasks/{id}/cancel for channel wakes (that path returns 409).
    const inboxEventId = task.inbox_event_id?.trim();
    if (!inboxEventId) {
      showErrorToast(t(($) => $.agent_status.stop_failed));
      return;
    }
    const toastName = displayName.trim();
    if (!toastName) {
      showErrorToast(t(($) => $.agent_status.stop_failed));
      return;
    }
    setStoppingChannelTaskId(task.task_id);
    try {
      await api.cancelChannelInboxEvent(active.id, inboxEventId);
      toast.success(
        t(($) => $.agent_status.stop_success, { name: toastName }),
      );
      qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(active.id) });
      qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    } catch {
      showErrorToast(t(($) => $.agent_status.stop_failed));
    } finally {
      setStoppingChannelTaskId((current) => (current === task.task_id ? null : current));
    }
  }, [active?.id, qc, t, wsId]);

  const handleStopAllChannelTasks = useCallback(async (tasks: ChannelActiveTask[]) => {
    if (!active?.id || tasks.length === 0) return;
    // LRM-425 — one bulk request; never for-in / Promise.all N× cancel.
    setStoppingChannelTaskId(STOPPING_ALL_TASKS_ID);
    try {
      const result = await api.cancelChannelActiveInboxEvents(active.id);
      const stopped = result.cancelled_count;
      if (stopped > 0) {
        toast.success(t(($) => $.agent_status.stop_all_success, { count: stopped }));
      } else {
        showErrorToast(t(($) => $.agent_status.stop_failed));
      }
    } catch {
      showErrorToast(t(($) => $.agent_status.stop_failed));
    }
    qc.invalidateQueries({ queryKey: activeChannelTasksKeys.all(active.id) });
    qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
    qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    setStoppingChannelTaskId(null);
    // LRM-405 — after stop, focus composer so the user can re-guide agents.
    // Do not auto-insert @mentions.
    editorRef.current?.focus();
  }, [active?.id, qc, t, wsId]);

  const openStopAllAgentsConfirm = useCallback(() => {
    if (!canPostInChannel || !hasStoppableChannelTasks || isStoppingAllChannelTasks) return;
    setStopAllConfirmOpen(true);
  }, [canPostInChannel, hasStoppableChannelTasks, isStoppingAllChannelTasks]);

  const confirmStopAllAgents = useCallback(() => {
    void handleStopAllChannelTasks(stoppableChannelTasks);
  }, [handleStopAllChannelTasks, stoppableChannelTasks]);

  const handleToggleChannelPin = (channel: Channel) => {
    setChannelPin.mutate(
      { channelId: channel.id, pinned: !channel.pinned_at },
      { onError: () => showErrorToast(t(($) => $.dm.action_failed)) },
    );
  };

  const handleMarkChannelUnread = (channelId: string) => {
    markChannelUnread.mutate(channelId, {
      onError: () => showErrorToast(t(($) => $.dm.action_failed)),
    });
  };

  const muteChannel = useMuteChannel();
  const setNotifyPreference = useSetChannelNotifyPreference();
  const [notifyPrefsOpen, setNotifyPrefsOpen] = useState(false);

  const handleToggleChannelMute = (channel: Channel) => {
    muteChannel.mutate(
      { channelId: channel.id, muted: !isConversationMuted(channel) },
      { onError: () => showErrorToast(t(($) => $.dm.action_failed)) },
    );
  };

  const handleShare = async () => {
    if (!active) return;
    const url = getShareableUrl(wsPaths.channelDetail(active.id));
    try {
      await navigator.clipboard.writeText(url);
      toast.success(t(($) => $.share.copied));
    } catch {
      showErrorToast(t(($) => $.share.copy_failed));
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

  // Alice #1984: after group send, invite undelivered @ targets (Raft-shaped).
  const promptInviteUndelivered = useCallback(
    (
      channelId: string,
      msg: {
        undelivered_mentions?: Array<{
          type: string;
          id: string;
          handle?: string;
          label?: string;
          actions?: string[];
        }>;
      },
    ) => {
      const invitees = inviteableUndeliveredMentions(msg.undelivered_mentions);
      if (invitees.length === 0) return;
      const names: string[] = [];
      const members: Array<{ member_type: "user" | "agent"; member_id: string }> =
        [];
      for (const m of invitees) {
        names.push(m.display);
        members.push({ member_type: m.member_type, member_id: m.member_id });
      }
      const who =
        names.length === 0
          ? String(invitees.length)
          : names.length <= 3
            ? names.join(", ")
            : `${names.slice(0, 3).join(", ")}…`;
      toast.message(t(($) => $.composer.undelivered_mentions_title), {
        description: t(($) => $.composer.undelivered_mentions_body, { who }),
        action: {
          label: t(($) => $.composer.undelivered_invite),
          onClick: () => {
            addMembers.mutate(
              { channelId, members },
              {
                onSuccess: () =>
                  toast.success(t(($) => $.composer.undelivered_invite_done)),
                onError: () => showErrorToast(t(($) => $.members.invite_failed)),
              },
            );
          },
        },
        duration: 12_000,
      });
    },
    [addMembers, t],
  );

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
    // #1276 INV-1 — capture the draft key at dispatch: onCommitted/onVisibleError
    // fire async, so a channel switch mid-flight must not clear/restore the wrong
    // channel's draft.
    const draftKey = activeDraftKey;
    const channelIdForSend = active.id;
    // Send lock (N held/auto-repeat Enter → 1 request) + payload-bound
    // client_message_id + the 3-way outcome, all owned by useComposerSend.
    const dispatched = channelSend.send({
      payloadKey: composePayloadKey(content, attachmentIds, quoteTarget?.id ?? ""),
      buildVars: (clientMessageId) => ({
        channelId: channelIdForSend,
        content,
        parts,
        quoteMessageId: quoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: (vars, cbs) => {
        sendMessage.mutate(vars, {
          ...cbs,
          onSuccess: (msg) => {
            promptInviteUndelivered(channelIdForSend, msg);
            cbs.onSuccess();
          },
        });
      },
      // #1276 INV-1: the input is cleared on optimistic dispatch (below), but the
      // PERSISTED draft is cleared ONLY here, on confirmed success — never on
      // dispatch. Optimistic UI may DISPLAY optimistically (empty composer +
      // pending bubble) but must not DELETE the user's text before the send
      // commits; otherwise a failure/abort (up to the 30s send timeout) or a
      // reload/channel-switch mid-flight loses it.
      onCommitted: () => {
        setChannelSendError(null);
        if (draftKey) storeClearComposerDraft(draftKey);
      },
      // #772: no permanent failed bubble. Restore the failed text into the
      // composer (unless it already holds new text → keep + offer Restore) and
      // show the inline error bar; bump the editor remount-nonce so it re-reads
      // the restored draft.
      onVisibleError: (kind) => {
        const currentText = editorRef.current?.getMarkdown()?.trim() ?? "";
        const conflicted = currentText.length > 0 && currentText !== content;
        channelFailedContentRef.current = content;
        if (!conflicted && draftKey) {
          setConversationDraft(draftKey, content);
          setChannelRestoreNonce((n) => n + 1);
        }
        setChannelSendError({ conflicted, tooLong: kind === "too_long" });
      },
    });
    if (dispatched) {
      setChannelSendError(null);
      prepareVoicePlayback(voicePlaybackScope(active.id));
      editorRef.current?.clearContent();
      channelPending.clear();
      setQuoteTarget(null);
      // NB: the persisted draft is intentionally NOT cleared here (#1276 INV-1) —
      // it is cleared in onCommitted (confirmed success) so the text survives any
      // non-success outcome, including a reload/switch during the in-flight window.
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }
  };

  // #838 — the submit itself, callable for the FIRST attempt and for a retry.
  // Retry must replay this exact action (Iris): same already-uploaded
  // attachment, real voice send path, never the text send and never a re-record.
  const submitChannelVoice = (
    channelId: string,
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ): boolean => {
    const content = "";
    const parts = buildRecordedVoiceMessageParts(durationMs, attachment);
    const dispatched = channelSend.send({
      payloadKey: composePayloadKey(content, [attachment.id], `voice:${quoteTarget?.id ?? ""}`),
      buildVars: (clientMessageId) => ({
        channelId,
        content,
        parts,
        quoteMessageId: quoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendMessage.mutate,
      // Only a send that actually committed clears the record.
      onCommitted: () => {
        settlePendingVoiceRetry(voiceTargetId(channelId));
        forgetPendingVoice(voiceTargetId(channelId));
      },
      onVisibleError: (kind) => {
        // The retry (if this was one) has settled — failed, but settled.
        settlePendingVoiceRetry(voiceTargetId(channelId));
        // EVERY failure kind lands here now. Previously only `conflict` said
        // anything, so a retry/timeout/too-long voice send — the common cases —
        // produced no feedback at all: the recording vanished silently.
        showErrorToast(
          kind === "too_long"
            ? t(($) => $.composer.send_failed_too_long)
            : t(($) => $.composer.send_failed),
        );
        // The toast is the announcement; THIS is the record. It survives the
        // toast being dismissed, and only a successful retry or an explicit
        // delete removes it (no timer, never overwritten by a new recording).
        rememberPendingVoice({
          targetId: voiceTargetId(channelId),
          channelId,
          durationMs,
          attachment,
        });
      },
    });
    if (dispatched) {
      setQuoteTarget(null);
      if (activeDraftKey) storeClearComposerDraft(activeDraftKey);
      if (typingStartedRef.current) {
        typingStartedRef.current = false;
        publishTyping(false);
      }
    }
    return dispatched;
  };

  // Compose-time entry: the guards here govern STARTING a voice send (composer
  // must be empty, no in-flight uploads) — plus #838's rule that an unsent
  // recording is never silently replaced: while one is pending, the user must
  // retry or delete it first.
  const handleVoiceSend = (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ): boolean => {
    if (!active || !activeDraftEmpty || channelPending.pending.length > 0) return false;
    if (channelPendingVoiceHere) return false;
    return submitChannelVoice(active.id, durationMs, attachment);
  };

  // Retry deliberately skips the compose-time guards: they exist to stop you
  // starting a NEW recording over unsent work, which is exactly what this is
  // resolving. The payload carries content:"" either way, so text typed since
  // the failure doesn't conflict with re-sending the recording.
  // Re-sends to the recording's OWN channel, not whatever is on screen now.
  const retryChannelVoice = () => {
    if (!channelPendingVoiceHere) return;
    // Mark BEFORE dispatching: the send can settle synchronously (a mocked or
    // already-cached mutation), and settling before the mark would leave the
    // record stuck in the in-flight state forever.
    setRetryingVoiceTargetId(channelPendingVoiceHere.targetId);
    const dispatched = submitChannelVoice(
      channelPendingVoiceHere.channelId,
      channelPendingVoiceHere.durationMs,
      channelPendingVoiceHere.attachment,
    );
    // The send lock can refuse the dispatch (held / auto-repeat trigger). No
    // request means nothing will settle it, so don't leave the mark behind.
    if (!dispatched) settlePendingVoiceRetry(channelPendingVoiceHere.targetId);
  };

  const handleThreadSend = () => {
    if (!active || !threadRoot) return;
    if (threadPending.hasUploading) return;
    const content = threadEditorRef.current?.getMarkdown()?.trim() ?? "";
    const parts = buildChatMessageParts(content, threadPending.readyAttachmentParts);
    if (parts.length === 0) return;
    const attachmentIds = threadPending.readyAttachmentParts.map((p) => p.attachment_id);
    const dispatched = threadSend.send({
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
      mutate: (vars, cbs) => {
        sendThreadMessage.mutate(vars, {
          ...cbs,
          onSuccess: (msg) => {
            promptInviteUndelivered(active.id, msg);
            cbs.onSuccess();
          },
        });
      },
      onCommitted: () => setChannelThreadSendError(null),
      onVisibleError: () => {
        // #772 (channel thread): restore failed text into the thread composer
        // (via editor defaultValue + remount) unless it holds new text; bar shown.
        const currentText = threadEditorRef.current?.getMarkdown()?.trim() ?? "";
        const conflicted = currentText.length > 0 && currentText !== content;
        channelThreadFailedContentRef.current = content;
        if (!conflicted) {
          setChannelThreadRestoreText(content);
          setChannelThreadRestoreNonce((n) => n + 1);
        }
        setChannelThreadSendError({ conflicted });
      },
    });
    if (dispatched) {
      setChannelThreadSendError(null);
      setChannelThreadRestoreText("");
      prepareVoicePlayback(voicePlaybackScope(active.id, threadRoot.id));
      threadEditorRef.current?.clearContent();
      threadPending.clear();
      setThreadQuoteTarget(null);
      setThreadDraftEmpty(true);
    }
  };

  // #838 — see submitChannelVoice: submit split out so a retry replays this
  // exact action with the same already-uploaded attachment.
  const submitThreadVoice = (
    channelId: string,
    threadRootId: string,
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ): boolean => {
    const content = "";
    const parts = buildRecordedVoiceMessageParts(durationMs, attachment);
    const dispatched = threadSend.send({
      payloadKey: composePayloadKey(
        content,
        [attachment.id],
        `${threadRootId}:voice:${threadQuoteTarget?.id ?? ""}`,
      ),
      buildVars: (clientMessageId) => ({
        channelId,
        messageId: threadRootId,
        content,
        parts,
        quoteMessageId: threadQuoteTarget?.id ?? undefined,
        clientMessageId,
      }),
      mutate: sendThreadMessage.mutate,
      onCommitted: () => {
        settlePendingVoiceRetry(voiceTargetId(channelId, threadRootId));
        forgetPendingVoice(voiceTargetId(channelId, threadRootId));
      },
      onVisibleError: (kind) => {
        settlePendingVoiceRetry(voiceTargetId(channelId, threadRootId));
        // #838 — same gap as the channel path: only `conflict` used to speak,
        // so retry/timeout/too-long failures lost the recording in silence.
        showErrorToast(
          kind === "too_long"
            ? t(($) => $.composer.send_failed_too_long)
            : t(($) => $.thread.send_failed),
        );
        rememberPendingVoice({
          targetId: voiceTargetId(channelId, threadRootId),
          channelId,
          threadRootId,
          durationMs,
          attachment,
        });
      },
    });
    if (dispatched) {
      setThreadQuoteTarget(null);
      setThreadDraftEmpty(true);
    }
    return dispatched;
  };

  // Compose-time entry (see handleVoiceSend for why the guards differ from retry).
  const handleThreadVoiceSend = (
    durationMs: number,
    attachment: VoiceRecordingAttachment,
  ): boolean => {
    if (!active || !threadRoot || !threadDraftEmpty || threadPending.pending.length > 0) return false;
    if (threadPendingVoiceHere) return false;
    return submitThreadVoice(active.id, threadRoot.id, durationMs, attachment);
  };

  // Re-sends to the recording's OWN channel + thread root.
  const retryThreadVoice = () => {
    if (!threadPendingVoiceHere) return;
    // Same ordering rule as the channel retry (see retryChannelVoice).
    setRetryingVoiceTargetId(threadPendingVoiceHere.targetId);
    const dispatched = submitThreadVoice(
      threadPendingVoiceHere.channelId,
      threadPendingVoiceHere.threadRootId ?? "",
      threadPendingVoiceHere.durationMs,
      threadPendingVoiceHere.attachment,
    );
    if (!dispatched) settlePendingVoiceRetry(threadPendingVoiceHere.targetId);
  };

  // #772 restore-previous (conflicted case: composer held new text).
  const handleRestorePrevious = () => {
    if (!activeDraftKey) return;
    setConversationDraft(activeDraftKey, channelFailedContentRef.current);
    setChannelRestoreNonce((n) => n + 1);
    setChannelSendError(null);
  };

  const handleRestoreChannelThreadPrevious = () => {
    setChannelThreadRestoreText(channelThreadFailedContentRef.current);
    setChannelThreadRestoreNonce((n) => n + 1);
    setChannelThreadSendError(null);
  };

  const handleRetrySend = useCallback(
    (message: ChannelMessage) => {
      if (!active || !message.client_message_id || message.local_send_status !== "failed") return;
      if (message.thread_root_message_id) {
        sendThreadMessage.mutate({
          channelId: active.id,
          messageId: message.thread_root_message_id,
          content: message.content,
          parts: message.parts,
          quoteMessageId: message.quote_message_id ?? undefined,
          clientMessageId: message.client_message_id,
        });
        return;
      }
      sendMessage.mutate({
        channelId: active.id,
        content: message.content,
        parts: message.parts,
        quoteMessageId: message.quote_message_id ?? undefined,
        clientMessageId: message.client_message_id,
      });
    },
    [active, sendMessage, sendThreadMessage],
  );

  // Stable identity is load-bearing, not cosmetic: areChannelMessageBubbleProps
  // Equal compares onOpenThread/onOpenAgent BY REFERENCE, so a fresh closure
  // on every render defeats every visible bubble's memo on every render.
  const handleOpenThread = useCallback((message: ChannelMessage) => {
    focusThreadComposerOnOpenRef.current = true;
    setSidePanel({ kind: "thread", message });
  }, []);

  const handleOpenAgentPanel: OpenAgentPanelFn = useCallback(
    (agentId, snapshot, options) => {
      // LRM-877 Dock Stack: open Agent on top of a human Profile without
      // losing the pop target. Explicit options win; else inherit from the
      // currently open member (or an existing agent stack frame).
      // LRM-740: also keep Thread under Profile/Agent so avatar clicks from
      // Thread (esp. Activity embed) don't fall through to a skeleton.
      setSidePanel((current) => {
        const returnToMemberId =
          options?.returnToMemberId ??
          (current.kind === "member"
            ? current.userId
            : current.kind === "agent"
              ? current.returnToMemberId
              : undefined);
        const returnToThread =
          current.kind === "thread"
            ? current.message
            : current.kind === "member" || current.kind === "agent"
              ? current.returnToThread
              : undefined;
        return {
          kind: "agent",
          agentId,
          snapshot,
          ...(returnToMemberId ? { returnToMemberId } : {}),
          ...(returnToThread ? { returnToThread } : {}),
        };
      });
    },
    [],
  );

  const handleOpenMemberPanel = useCallback((userId: string) => {
    setSidePanel((current) => {
      const returnToThread =
        current.kind === "thread"
          ? current.message
          : current.kind === "agent"
            ? current.returnToThread
            : undefined;
      return {
        kind: "member",
        userId,
        ...(returnToThread ? { returnToThread } : {}),
      };
    });
  }, []);

  const handlePopAgentToMember = useCallback(() => {
    setSidePanel((current) => {
      if (current.kind === "agent" && current.returnToMemberId) {
        return {
          kind: "member",
          userId: current.returnToMemberId,
          ...(current.returnToThread
            ? { returnToThread: current.returnToThread }
            : {}),
        };
      }
      if (current.kind === "agent" && current.returnToThread) {
        return { kind: "thread", message: current.returnToThread };
      }
      return { kind: "none" };
    });
  }, []);

  // #645 — toggles the same exclusive slot; opening it always wins over
  // thread/agent (mirrors handleOpenAgentPanel), closing just clears it.
  // Desktop title click collapses the dock whenever it is open (any tab) —
  // otherwise Settings → title would only switch back to About and leave
  // keep-alive TabsContent (LRM-400) still exposing Settings controls.
  const toggleChannelDetails = (tab: ChannelDetailsTab = "about") => {
    if (isMobile || isHeaderActionsCompact) {
      if (mobilePanel !== null) {
        setMobilePanel(null);
        return;
      }
      openChannelDetails(tab);
      return;
    }
    if (channelDetailsOpen) {
      closeChannelDetails();
      return;
    }
    openChannelDetails(tab);
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
        onSuccess: () => {
          setSelectedInvites(new Set());
          setInviteQuery("");
          setAddPeopleDialogOpen(false);
        },
        onError: () => showErrorToast(t(($) => $.members.invite_failed)),
      },
    );
  };

  const roleForChannelMember = useCallback(
    (m: ChannelMember): MemberRoleLabel => {
      if (m.member_type === "agent") return "agent";
      const role = workspaceMembers.find((w) => w.user_id === m.member_id)?.role;
      if (role === "owner" || role === "admin") return role;
      return "member";
    },
    [workspaceMembers],
  );

  // The group member panel shows the CHANNEL/group role badge (owner / 群管 /
  // 管理员), which REPLACES the workspace-role label. Gated to ordinary
  // non-system groups only: `general` is kind=group + system_key, and a system
  // channel must never paint a "群主" badge — a product-impossible concept — even
  // if BE returns a role. Fail closed on the display side; do not trust upstream
  // data (Parker/Iris alignment #multica:f8b8c656). DMs (kind!=="group") also
  // keep the workspace badge.
  const groupBadgeForMember =
    active?.kind === "group" && !isActiveSystemChannel
      ? channelMemberBadge
      : undefined;

  // The viewer's OWN channel role (owner|manager|member) drives the management
  // menu. Fail-closed: absent membership / missing role → "member".
  const viewerChannelRole = useMemo(
    () =>
      channelMemberRole(
        channelMembers.find(
          (m) => m.member_type === "user" && m.member_id === currentUserId,
        ) ?? {},
      ),
    [channelMembers, currentUserId],
  );
  // Group-managed surface: supply the menu for EVERY viewer of an ordinary
  // non-system group (same fail-closed gate as the badge). Who may do what lives
  // entirely inside `groupMemberActions`; a viewer it grants nothing gets no ⋯
  // trigger. #845: that is no longer owner-only — a manager may remove ordinary
  // members (and only that), so both the trigger and the removal path are now
  // reachable for managers. Crucially, the mere PRESENCE of `memberMenu` tells
  // the row this is a group-managed surface where the legacy workspace-admin
  // Remove no longer applies — removal goes through this menu (→ real mutation
  // with #801). This closes the bypass where a non-channel-owner workspace
  // admin, or the owner's own row, still got the old real Remove.
  // #833: an ARCHIVED group gets no management menu at all. Removal is a real
  // mutation now (see handleGroupMemberAction below), and an archived channel is
  // read-only — offering an operable "remove member" there would be an action we
  // shouldn't honour. #832: the role rows are real mutations now too, so the
  // same reasoning covers them — an archived group must not offer promote,
  // demote or transfer either. Gating the whole menu remains correct, but the
  // reason is now "every item in it writes", not "the role rows are inert".
  const groupMemberMenu = useMemo(
    () =>
      active?.kind === "group" && !isActiveSystemChannel && !isActiveArchived
        ? (m: ChannelMember) =>
            resolveGroupMemberActions(
              groupMemberActions({ role: viewerChannelRole }, m, currentUserId ?? ""),
              m,
              memberCapabilitiesByKey,
            )
        : undefined,
    [
      active?.kind,
      isActiveSystemChannel,
      isActiveArchived,
      viewerChannelRole,
      currentUserId,
      memberCapabilitiesByKey,
    ],
  );

  // Leave-group affordance for the channel-details danger zone. Ordinary
  // non-system, non-archived groups only (DM / system / archived → none). BE
  // #1286 (Nash) confirmed self-leave = DELETE .../members/user/{selfId}, and a
  // sole human owner leaving returns a server-authoritative 409. So a
  // confidently-detected SOLE human owner is pre-disabled with "transfer first"
  // (Nash/Iris prefer disabled over a click that 409s); everyone else — member,
  // manager, or a non-sole owner — gets a real clickable self-leave. The 409 is
  // the backstop when role data is too thin to detect sole-ownership up front:
  // it surfaces the same transfer-first message rather than a fake success.
  const handleLeaveGroup = useCallback(() => {
    if (!active || !currentUserId) return;
    const channelId = active.id;
    removeMember.mutate(
      { channelId, memberType: "user", memberId: currentUserId },
      {
        onSuccess: () => {
          if (channelId === activeId) setActiveId(null);
          toast.success(t(($) => $.details.leave_success));
        },
        onError: (err) => {
          showErrorToast(
            err instanceof ApiError && err.status === 409
              ? t(($) => $.details.leave_owner_reason)
              : t(($) => $.details.leave_failed),
          );
        },
      },
    );
  }, [active, activeId, currentUserId, removeMember, t]);
  const groupLeaveAffordance = useMemo(() => {
    if (active?.kind !== "group" || isActiveSystemChannel || active?.archived_at) {
      return undefined;
    }
    const humanOwnerCount = channelMembers.filter(
      (m) => m.member_type === "user" && channelMemberRole(m) === "owner",
    ).length;
    const isSoleOwner = viewerChannelRole === "owner" && humanOwnerCount <= 1;
    return isSoleOwner
      ? { disabledReason: t(($) => $.details.leave_owner_reason) }
      : { onLeave: handleLeaveGroup };
  }, [
    active?.kind,
    active?.archived_at,
    isActiveSystemChannel,
    viewerChannelRole,
    channelMembers,
    handleLeaveGroup,
    t,
  ]);

  // LRM-1195 — dismiss the mobile "…" Drawer before opening Add people.
  // Same Vaul lock as LRM-265 archive/delete: body gets pointer-events:none
  // while DrawerContent alone is unlocked; ChannelAddPeopleDialog portals to
  // body as a sibling, so the search input inherits the lock and cannot
  // focus. Closing first also avoids stacked-modal focus traps; Dialog itself
  // sets pointer-events:auto as defense-in-depth while the drawer animates out.
  const openAddPeopleDialog = useCallback(() => {
    setMobilePanel(null);
    setInviteQuery("");
    setDebouncedInviteQuery("");
    setSelectedInvites(new Set());
    setAddPeopleDialogOpen(true);
  }, []);

  // Removing someone is irreversible, so it ALWAYS goes through the confirm
  // step — desktop included. This used to mutate immediately on desktop and only
  // show the confirm Sheet on mobile; the menu rewire below made that immediate
  // path the primary desktop affordance, i.e. "click a menu item and the person
  // is gone". (Iris, #833 review.)
  //
  // One confirmation state and ONE mutate call site, shared by both platforms —
  // deliberately not a second desktop-only dialog, because two destructive paths
  // drift apart. This handler now only records the target; the actual removal
  // (and its failure toast) lives in the confirm action.
  const handleRemoveMemberClick = useCallback((m: ChannelMember) => {
    setRemoveMemberTarget(m);
  }, []);

  // #833 — the menu's "remove" used to land on a no-op toast while the real
  // Remove button was suppressed (it renders only when there is no menu), so the
  // two entry points cancelled out and a group owner could not remove anyone at
  // all. Route it to the SAME handler the standalone button used: identical
  // permission gate (`canRemove` from core), identical confirm step.
  //
  // #832 — promote / demote / transfer are now real mutations and DO reach this
  // handler: transfer opens its confirmation first (it gives away the viewer's
  // own owner rights), promote and demote go straight to `runRoleChange`. They
  // are no longer disabled placeholders, so every item in this menu writes.
  // #832 — role-change failures and in-flight state, keyed per member exactly
  // like the removal ones: a second failure must never silently replace an
  // unresolved first one, and one member's state must never render on another.
  const [roleFailures, setRoleFailures] = useState<
    ReadonlyMap<string, RoleChangeFailure>
  >(() => new Map());
  const [rolePending, setRolePending] = useState<
    ReadonlyMap<string, "promote" | "demote" | "transfer">
  >(() => new Map());
  const updateMemberRole = useUpdateChannelMemberRole();
  const transferOwnership = useTransferChannelOwnership();

  const runRoleChange = useCallback(
    (m: ChannelMember, action: "promote" | "demote" | "transfer") => {
      const channelId = active?.id;
      if (!channelId) return;
      const key = memberFailureKey(channelId, m);
      lastRoleActionRef.current?.set(key, action);
      setRolePending((prev) => new Map(prev).set(key, action));
      setRoleFailures((prev) => {
        if (!prev.has(key)) return prev;
        const next = new Map(prev);
        next.delete(key);
        return next;
      });
      // Transfer is a DIFFERENT ENDPOINT, not a different role value: the
      // member-role PATCH rejects `owner` outright (channel.go:1761), so sending
      // it there 400s every time. Caught by Felix in review — and invisible to
      // the suite, because the api mock accepts any arguments. The request
      // itself is now asserted (path + verb), not just "the mutation ran".
      const settle = {
          onSuccess: () => {
            setRolePending((prev) => {
              const next = new Map(prev);
              next.delete(key);
              return next;
            });
            toast.success(
              action === "transfer"
                ? t(($) => $.members.menu.role_done_transfer)
                : action === "promote"
                  ? t(($) => $.members.menu.role_done_promote)
                  : t(($) => $.members.menu.role_done_demote),
            );
          },
          onError: (error: unknown) => {
            setRolePending((prev) => {
              const next = new Map(prev);
              next.delete(key);
              return next;
            });
            // Classified, never message-matched: the server's text is a
            // hard-coded locale we don't control (#844 boundary).
            setRoleFailures((prev) =>
              new Map(prev).set(key, classifyRoleChangeFailure(error)),
            );
          },
      };
      if (action === "transfer") {
        transferOwnership.mutate(
          { channelId, memberType: m.member_type, memberId: m.member_id },
          settle,
        );
        return;
      }
      updateMemberRole.mutate(
        {
          channelId,
          memberType: m.member_type,
          memberId: m.member_id,
          role: action === "promote" ? "manager" : "member",
        },
        settle,
      );
    },
    [active?.id, updateMemberRole, transferOwnership, t],
  );

  // #832 — one sentence per failure kind. Copy is frontend-supplied in four
  // locales; the server's message is never rendered (it is hard-coded to one
  // language and cannot know the viewer's).
  const roleFailureFor = useCallback(
    (m: ChannelMember) => {
      const key = memberFailureKey(active?.id ?? "", m);
      const kind = roleFailures.get(key);
      if (!kind) return undefined;
      const dismiss = () =>
        setRoleFailures((prev) => {
          const next = new Map(prev);
          next.delete(key);
          return next;
        });
      const message =
        kind === "owner_changed"
          ? t(($) => $.members.menu.role_failed_owner_changed)
          : kind === "forbidden"
            ? t(($) => $.members.menu.role_failed_forbidden)
            : kind === "gone"
              ? t(($) => $.members.menu.role_failed_gone)
              : kind === "conflict" || kind === "contract"
                ? t(($) => $.members.menu.role_failed_generic)
                : t(($) => $.members.menu.role_failed_retryable);
      // Retry only where repeating the same call can succeed. For a moved
      // roster or a denial, a retry button would promise what it can't deliver.
      const retryable = kind === "transient";
      return {
        message,
        dismissLabel: t(($) => $.members.menu.role_failed_dismiss),
        onDismiss: dismiss,
        ...(retryable
          ? {
              retryLabel: t(($) => $.members.menu.role_failed_retry),
              onRetry: () => {
                dismiss();
                const last = lastRoleActionRef.current?.get(key);
                if (last) runRoleChange(m, last);
              },
            }
          : {}),
      };
    },
    [active?.id, roleFailures, t, runRoleChange],
  );

  const rolePendingActionFor = useCallback(
    (m: ChannelMember) => rolePending.get(memberFailureKey(active?.id ?? "", m)) ?? null,
    [active?.id, rolePending],
  );

  const handleGroupMemberAction = useCallback(
    (m: ChannelMember, action: GroupMemberActionKind) => {
      if (action === "remove") return handleRemoveMemberClick(m);
      // Transfer hands away the viewer's own owner rights immediately, so it
      // asks first. Promote/demote are ordinary edits the owner can undo from
      // the same menu, so they don't (Iris: confirm because you lose something
      // now, NOT because it's irreversible — the new owner can transfer back).
      if (action === "transfer") return setTransferTarget(m);
      runRoleChange(m, action);
    },
    [handleRemoveMemberClick, runRoleChange],
  );

  // LRM-211 — Channel details Members tab reuses the same list as the
  // Members dialog (no dual-tab Popover). LRM-225 — match dialog chrome
  // (search + Add people colors) so mobile/desktop don't diverge.
  // #839 — per-member failure notice. `onRetry` re-opens the named confirmation
  // (never calls the mutation): the confirmation stays the single destructive
  // commitment point, so a retry can never remove someone with one click.
  const removeFailureFor = useCallback(
    (m: ChannelMember) => {
      if (!removeFailedKeys.has(memberFailureKey(active?.id ?? "", m))) return undefined;
      return {
        message: t(($) => $.members.remove_failed),
        retryLabel: t(($) => $.members.remove_retry),
        dismissLabel: t(($) => $.members.remove_failed_dismiss),
        onRetry: () => setRemoveMemberTarget(m),
        onDismiss: () =>
          setRemoveFailedKeys((prev) => {
            const next = new Set(prev);
            next.delete(memberFailureKey(active?.id ?? "", m));
            return next;
          }),
      };
    },
    [removeFailedKeys, active?.id, t],
  );

  const memberPanelBody = active ? (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-2.5 border-b border-border px-4 py-3">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={membersQuery}
            onChange={(e) => setMembersQuery(e.target.value)}
            placeholder={t(($) => $.members.find_members)}
            className="h-10 rounded-lg border-input bg-muted/40 pl-9"
          />
        </div>
        {!isActiveSystemChannel && canArchive(active) && (
          <Button
            type="button"
            className="h-9 shrink-0 rounded-lg bg-brand px-3.5 text-sm font-semibold text-brand-foreground hover:bg-brand/90"
            onClick={openAddPeopleDialog}
          >
            {t(($) => $.members.add_people)}
          </Button>
        )}
      </div>
      <ChannelMembersList
        members={filteredMembers}
        loading={membersPending}
        emptyLabel={
          membersQuery.trim()
            ? t(($) => $.members.no_results)
            : t(($) => $.members.empty)
        }
        noResultsLabel={t(($) => $.members.no_results)}
        roleForMember={roleForChannelMember}
        badgeForMember={groupBadgeForMember}
        memberMenu={groupMemberMenu}
        onGroupMemberAction={handleGroupMemberAction}
        canRemove={!isActiveSystemChannel && canArchive(active)}
        isMobile={isMobile}
        currentUserId={currentUserId ?? ""}
        onOpenDm={openDmWithMember}
        onOpenAgent={handleOpenAgentPanel}
        onOpenMember={(userId) => {
          handleOpenMemberPanel(userId);
        }}
        onRemove={handleRemoveMemberClick}
        removeFailureFor={removeFailureFor}
        roleFailureFor={roleFailureFor}
        rolePendingActionFor={rolePendingActionFor}
        dmPending={createOrFindDm.isPending}
        className="min-h-0 flex-1"
      />
    </div>
  ) : null;


  // Shared channel row for the unified PINNED section and the CHANNELS list.
  const renderChannelSidebarRow = (channel: Channel) => {
    const realUnread = channel.real_unread_count ?? channel.unread_count ?? 0;
    const isManualDot = !!channel.manually_unread && realUnread === 0;
    const isMuted = isConversationMuted(channel);
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
                    onClick={() => showErrorToast(t(($) => $.sidebar.archive_permission))}
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
                active?.id === channel.id
                  ? CONVERSATION_SIDEBAR_ROW_ACTIVE
                  : CONVERSATION_SIDEBAR_ROW_IDLE,
              )}
            />
          }
        >
          <button
            type="button"
            onClick={() => selectChannel(channel.id)}
            className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left"
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center justify-between gap-2">
                <span
                  className={cn(
                    "flex min-w-0 items-center gap-1 truncate text-sm text-foreground",
                    // Slack-style: an unread channel reads as a BOLD name
                    // (#3); LRM-767 extends the bold to muted rows too — the
                    // bold name is then their only unread signal (no badge).
                    realUnread > 0 ? "font-semibold" : "font-medium",
                  )}
                >
                  {pinned && (
                    <Pin className="size-3 shrink-0 -rotate-45 fill-muted-foreground/70 text-muted-foreground/70" />
                  )}
                  {/* LRM-254 A1 — text-level # landmark; no member collage.
                      LRM-724 — custom channel icon replaces the glyph. */}
                  <ChannelHashLandmark size="sm" avatarUrl={channel.avatar_url} />
                  <span className="truncate">{channel.name}</span>
                  {isMuted && (
                    <MutedIndicator label={t(($) => $.sidebar.muted_label)} />
                  )}
                  {channel.lark_chat_id && (
                    <Smartphone className="size-3 shrink-0 text-emerald-600" />
                  )}
                </span>
                <ConversationUnreadAffordance
                  realUnread={realUnread}
                  isManualDot={isManualDot}
                  isMuted={isMuted}
                  unreadLabel={t(($) => $.sidebar.unread_indicator, {
                    count: realUnread,
                  })}
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
                          onClick={() => showErrorToast(t(($) => $.sidebar.archive_permission))}
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
  // desktop. LRM-551 lock A: same chrome plane as app sidebar (`bg-sidebar`);
  // 1px `border-r border-border` only beside the stream pane (dropped on
  // mobile where the list stands alone). Search is explicit `bg-background`
  // so it reads as an inset white field on sidebar chrome.
  const listPane = (
    <aside
      className={cn(
        "flex flex-1 min-h-0 flex-col bg-sidebar",
        isMobile ? "min-w-0" : "border-r border-border",
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
                className="h-9 bg-background pl-8"
              />
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
            {/* Unified PINNED section (Slack Starred / 置顶分组) — DMs + channels */}
            <PinnedConversationsSection entries={filteredPinnedEntries}>
              {filteredPinnedEntries.map((entry) => {
                if (entry.kind === "dm") {
                  const dm = entry.dm;
                  const bubble = dmAgentBubbleActivity(dm, bubbleActivityByAgent);
                  return (
                    <DmConversationRow
                      key={`pinned-dm:${dm.source}:${dm.id}`}
                      dm={dm}
                      active={activeDmId === dm.id}
                      currentUserName={currentUserName}
                      resolveMentionPreview={resolveMentionPreview}
                      members={workspaceMembers}
                      agents={agents}
                      bubbleUnreadCount={bubble?.unreadCount ?? 0}
                      bubbleLatestUpdatedAt={bubble?.latestUpdatedAt ?? null}
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
                    <span className={CONVERSATION_SIDEBAR_UNREAD_BADGE}>
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
                    {/* #576 — optional project binding at creation time. Same
                        ProjectPickerButton + PropRow pattern the group-settings
                        Project section uses (ChannelProjectSettingsPanel);
                        leaving it unset behaves exactly like the pre-existing
                        create flow. */}
                    <div className="grid min-w-0 grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
                      <PropRow label={t(($) => $.composer.project_label)} interactive={false}>
                        <div className="flex min-w-0 items-center gap-1.5">
                          <span className="min-w-0 truncate text-xs text-foreground">
                            {workspaceProjects.find((p) => p.id === newProjectId)?.title ??
                              t(($) => $.composer.project_none)}
                          </span>
                          <ProjectPickerButton
                            wsId={wsId}
                            value={newProjectId}
                            onChange={setNewProjectId}
                            label={t(($) => $.composer.project_label)}
                            noneLabel={t(($) => $.composer.project_none)}
                            tooltip={t(($) => $.composer.project_tooltip)}
                          />
                        </div>
                      </PropRow>
                    </div>
                    <Button className="w-full" onClick={handleCreate} disabled={createChannel.isPending}>
                      {t(($) => $.sidebar.create_aria)}
                    </Button>
                  </PopoverContent>
                </Popover>
              </div>

              {!channelsCollapsed && (
                // LRM-459: isPending (not isLoading) — avoids empty-state flash
                // when the query is enabled:false or idle before first fetch.
                channelsPending ? (
                  <ChannelListSkeleton />
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
                      const deleteAllowed = canDeleteChannel(channel);
                      const archivedDeleteItem = deleteAllowed ? (
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() => setDeleteTarget(channel)}
                        >
                          <Trash2 className="size-4" />
                          {t(($) => $.sidebar.delete)}
                        </DropdownMenuItem>
                      ) : null;
                      const archivedDeleteContextItem = deleteAllowed ? (
                        <ContextMenuItem
                          variant="destructive"
                          onClick={() => setDeleteTarget(channel)}
                        >
                          <Trash2 className="size-4" />
                          {t(($) => $.sidebar.delete)}
                        </ContextMenuItem>
                      ) : null;
                      return (
                        <ContextMenu key={channel.id}>
                          <ContextMenuTrigger
                            render={
                              <div className="group/archived relative mb-0.5 rounded-lg transition-colors hover:bg-sidebar-accent" />
                            }
                          >
                            <button
                              type="button"
                              onClick={() => selectChannel(channel.id)}
                              data-testid="channel-sidebar-archived-row"
                              // LRM-1374: archived softening is solid muted title —
                              // never row opacity-* (alpha multiplies through name).
                              className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 pr-7 text-left"
                            >
                              <div className="min-w-0 flex-1">
                                <span className="flex min-w-0 items-center gap-1 truncate text-sm font-medium text-muted-foreground">
                                  <ChannelHashLandmark size="sm" avatarUrl={channel.avatar_url} />
                                  <span className="truncate">{channel.name}</span>
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
                                        onClick={() => showErrorToast(t(($) => $.sidebar.restore_permission))}
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
                                {archivedDeleteItem}
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
                                    onClick={() => showErrorToast(t(($) => $.sidebar.restore_permission))}
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
                            {archivedDeleteContextItem}
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
    channelsPending || (!!activeId && !activeDmId && !active);
  // The thread surface is the shared <ThreadPanel> (pinned root + flat replies +
  // participant chips + wake strip), fed the #251 read-model off the root
  // message. also-send is CUT this round (#256), so no also-send props are
  // passed — the panel then hides the checkbox entirely.
  const threadPanel =
    active && threadSurfaceRoot ? (
      <Suspense fallback={<ChannelLazyPanelFallback />}>
      <ThreadPanel
        root={threadSurfaceRoot}
        replies={threadReplies}
        currentUserId={currentUserId}
        currentUserName={currentUserName ?? undefined}
        isMobile={isMobile}
        onBack={() => {
          // Activity embed pins the thread surface — dismissing to an empty
          // right pane would resurrect the forbidden Select-a-notification
          // gap. Leave dismissal to Activity's list / mobile Back.
          if (embedded && embeddedSurface === "thread") return;
          setOpenThreadRoot(null);
        }}
        followed={threadSurfaceRoot.thread_followed === true}
        followDisabled={
          threadLoading ||
          (setThreadFollowed.isPending &&
            setThreadFollowed.variables?.messageId === threadSurfaceRoot.id)
        }
        onFollowChange={handleThreadFollowChange}
        parentContext="channel"
        parentChannelName={active.name}
        // react-doctor-disable-next-line react-doctor/jsx-no-new-function-as-prop -- thread surface keyed on root; parent jump closure is not memo-sensitive
        onViewParent={() => {
          if (embedded) {
            if (!onOpenInChannels) {
              showErrorToast(t(($) => $.thread.view_in_channel_failed));
              return;
            }
            onOpenInChannels({
              channelId: threadSurfaceRoot.channel_id,
              messageId: threadSurfaceRoot.id,
            });
            return;
          }
          // LRM-572 / LRM-389 — close the side panel so the parent main column
          // is opened and the root is scrolled/highlighted (header「在 #频道 查看」
          // and root「查看原消息」share this handler).
          setHighlightMessageId(threadSurfaceRoot.id);
          setOpenThreadRoot(null);
        }}
        loading={threadLoading}
        loadError={threadError}
        onRetry={() => refetchThread()}
        highlightMessageId={isThreadDeepLink ? highlightMessageId : undefined}
        onReact={handleReactToMessage}
        onQuoteMessage={setThreadQuoteTarget}
        onInsertSelectionQuote={(md: string) =>
          threadEditorRef.current?.insertMarkdown(md)
        }
        onRetrySend={handleRetrySend}
        onOpenAgent={handleOpenAgentPanel}
        onOpenMember={handleOpenMemberPanel}
        quoteTarget={threadQuoteTarget}
        onClearQuote={() => setThreadQuoteTarget(null)}
        sendError={channelThreadSendError}
        // react-doctor-disable-next-line react-doctor/jsx-no-new-function-as-prop -- thread send helpers recreate with surface state; panel remounts with root
        onRestorePrevious={handleRestoreChannelThreadPrevious}
        editor={
          <ContentEditor
            key={`thread-editor:${threadSurfaceRoot.id}:${channelThreadRestoreNonce}`}
            ref={threadEditorRef}
            defaultValue={channelThreadRestoreText}
            // Bare URLs stay plain text in the composer (#531/#542).
            plainUrls
            placeholder={t(($) => $.thread.composer_placeholder)}
            onUpdate={handleThreadEditorUpdate}
            onSubmit={handleThreadSend}
            mediaMode="external"
            onExternalFiles={threadPending.addFiles}
            submitOnEnter
            showBubbleMenu={false}
            mentionAllowedActorIds={mentionAllowedActorIds}
            scopedMentionAgents={channelAgentCandidates}
            mentionChannelMemberIds={
              active?.kind === "group" ? channelMemberIds : null
            }
          />
        }
        // react-doctor-disable-next-line react-doctor/jsx-no-new-function-as-prop -- same thread send helper as onRestorePrevious
        onSend={handleThreadSend}
        voicePlaybackScope={voicePlaybackScope(active.id, threadSurfaceRoot.id)}
        // react-doctor-disable-next-line react-doctor/jsx-no-new-object-as-prop -- voice gate flags; ThreadPanel is not memoized on this object
        voiceBlock={{
          pendingVoice: !!threadPendingVoiceHere,
          hasTextDraft: !threadDraftEmpty,
          hasAttachmentDraft: threadPending.pending.length > 0,
        }}
        // react-doctor-disable-next-line react-doctor/jsx-no-new-function-as-prop -- thread voice send recreates with draft/pending
        onVoiceSend={handleThreadVoiceSend}
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer prefix slot; identity is not memo-sensitive
        composerPrefixExtra={
          <ComposerPendingVoice
            pending={threadPendingVoiceHere}
            retrying={
              threadPendingVoiceHere !== null &&
              retryingVoiceTargetId === threadPendingVoiceHere.targetId
            }
            onRetry={retryThreadVoice}
            onDelete={() =>
              threadPendingVoiceHere && forgetPendingVoice(threadPendingVoiceHere.targetId)
            }
          />
        }
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
      />
      </Suspense>
    ) : null;
  const agentPanelBackLabel = selectedAgentReturnToMemberId
    ? resolveActorDisplayName(
        workspaceMembers.find((m) => m.user_id === selectedAgentReturnToMemberId) ??
          null,
        selectedAgentReturnToMemberId,
      )
    : undefined;
  const agentPanel =
    active && selectedAgentPanelId ? (
      <Suspense fallback={<ChannelLazyPanelFallback />}>
      <ResolvedAgentSidePanel
        agentId={selectedAgentPanelId}
        identitySnapshot={selectedAgentPanelSnapshot}
        currentUserId={currentUserId}
        members={workspaceMembers}
        onClose={() => setSelectedAgentPanelId(null)}
        variant={isMobile ? "page" : "panel"}
        onBack={
          selectedAgentReturnToMemberId ? handlePopAgentToMember : undefined
        }
        backLabel={agentPanelBackLabel}
      />
      </Suspense>
    ) : null;
  const memberPanel =
    active && selectedMemberPanelId ? (
      <Suspense fallback={<ChannelLazyPanelFallback />}>
      <MemberSidePanel
        userId={selectedMemberPanelId}
        onClose={() => setSelectedMemberPanelId(null)}
        variant={isMobile ? "page" : "panel"}
        doneLabel={isMobile ? tAgents(($) => $.side_panel.back_to_messages) : undefined}
        // react-doctor-disable-next-line react-doctor/jsx-no-new-function-as-prop -- member→DM handoff; panel unmounts on navigate
        onMessage={(userId) => {
          createOrFindDm.mutate(
            { peer_type: "user", peer_id: userId },
            {
              onSuccess: (dm) => {
                // Leaving for a DM — drop any Thread return stack.
                setSidePanel({ kind: "none" });
                selectDm(dm);
              },
              onError: () => showErrorToast(t(($) => $.dm.open_failed)),
            },
          );
        }}
      />
      </Suspense>
    ) : null;
  // LRM-210 — Channel details panel (About|Members|Files|Settings). System
  // #general still opens About/Members/Files (read-only roster) but hides
  // the Settings tab — same defense-in-depth as the old settings gate.
  const detailsPanelProps = active
    ? {
        channel: active,
        members: channelMembers,
        groupLeave: groupLeaveAffordance,
        wsId,
        projectId: channelProjectId || null,
        onChangeProject: (projectId: string | null) => setChannelProject.mutate(projectId),
        projectDisabledReason,
        access: {
          canManage: canArchive(active),
          isArchived: isActiveArchived,
          hideSettingsTab: isActiveSystemChannel,
          projectBound: !!channelProjectId,
          projectEditable,
          mutePending: muteChannel.isPending,
          renamePending: updateChannel.isPending,
          descriptionPending: updateChannel.isPending,
          larkPending: updateChannel.isPending,
          avatarPending: updateChannel.isPending,
          stopAllDisabled: !hasStoppableChannelTasks || isStoppingAllChannelTasks,
        },
        manageDisabledReason,
        onMuteToggle: () => handleToggleChannelMute(active),
        onShare: () => {
          void handleShare();
        },
        // LRM-265 — dismiss the mobile "…" Drawer before opening archive /
        // delete AlertDialogs. Matches the Members entry (closes panel then
        // opens its dialog). Leaving the Vaul modal open keeps
        // `body.style.pointerEvents = "none"`; AlertDialog portals to body
        // (sibling of DrawerContent), so checkbox / actions inherit the lock
        // and become unclickable. Closing first also avoids stacked-modal
        // focus traps; AlertDialog itself also sets `pointer-events-auto` as
        // defense-in-depth while the drawer animates out.
        onArchive: () => {
          setMobilePanel(null);
          setArchiveTarget(active);
        },
        onDelete: canDeleteChannel(active)
          ? () => {
              setMobilePanel(null);
              setDeleteTarget(active);
            }
          : undefined,
        onRename: (name: string) => {
          updateChannel.mutate(
            { channelId: active.id, name },
            {
              onSuccess: () => toast.success(t(($) => $.settings.rename_success)),
              onError: () => showErrorToast(t(($) => $.settings.rename_failed)),
            },
          );
        },
        onUpdateDescription: (description: string | null) => {
          updateChannel.mutate(
            { channelId: active.id, description },
            {
              onSuccess: () => toast.success(t(($) => $.settings.description_success)),
              onError: () => showErrorToast(t(($) => $.settings.description_failed)),
            },
          );
        },
        onUpdateLarkChatId: (larkChatId: string | null) => {
          updateChannel.mutate(
            { channelId: active.id, lark_chat_id: larkChatId },
            {
              onSuccess: () => toast.success(t(($) => $.settings.lark_success)),
              onError: () => showErrorToast(t(($) => $.settings.lark_failed)),
            },
          );
        },
        onUpdateAvatar: (avatarUrl: string) => {
          updateChannel.mutate(
            { channelId: active.id, avatar_url: avatarUrl },
            {
              onSuccess: () => toast.success(t(($) => $.settings.avatar_success)),
              onError: () => showErrorToast(t(($) => $.settings.avatar_failed)),
            },
          );
        },
        membersBody: memberPanelBody,
        onClose: closeChannelDetails,
        onOpenSearch: () => setConvSearchOpen(true),
        onStopAllAgents: canPostInChannel
          ? () => {
              openStopAllAgentsConfirm();
            }
          : undefined,
        stopAllDisabledReason: hasStoppableChannelTasks
          ? undefined
          : t(($) => $.stop_all_agents.empty_tooltip),
        notifyPrefLabel: channelNotifyLevelLabel(
          t,
          resolveChannelNotifyLevel(active!),
        ),
        notifyLevel: resolveChannelNotifyLevel(active!),
        onSelectNotifyLevel: (level: ChannelNotifyLevel) => {
          setNotifyPreference.mutate(
            { channelId: active!.id, level },
            { onError: () => showErrorToast(t(($) => $.dm.action_failed)) },
          );
        },
        notifyLevelPending: setNotifyPreference.isPending,
        onOpenGlobalNotifySettings: () => {
          push(`${wsPaths.settings()}?tab=notifications`);
        },
        onOpenNotificationPrefs: () => {
          setNotifyPrefsOpen(true);
        },
      }
    : null;
  const detailsPanel =
    channelDetailsOpen && detailsPanelProps ? (
      <Suspense fallback={<ChannelLazyPanelFallback />}>
      <ChannelDetailsPanel
        key={`${active!.id}:${channelDetailsTab}`}
        {...detailsPanelProps}
        initialTab={channelDetailsTab}
        variant="panel"
      />
      </Suspense>
    ) : null;
  // LRM-748 frozen v2 — desktop notify prefs live in this dialog (never a
  // page push); mobile drills into the details panel's internal sub-view.
  const notifyPrefsDialog = active ? (
    <ChannelNotifyPrefsDialog
      open={notifyPrefsOpen}
      onOpenChange={setNotifyPrefsOpen}
      channelName={active.name}
      level={resolveChannelNotifyLevel(active)}
      pending={setNotifyPreference.isPending}
      onSelect={(level) => {
        setNotifyPreference.mutate(
          { channelId: active.id, level },
          { onError: () => showErrorToast(t(($) => $.dm.action_failed)) },
        );
      }}
      onOpenGlobalSettings={() => {
        setNotifyPrefsOpen(false);
        push(`${wsPaths.settings()}?tab=notifications`);
      }}
    />
  ) : null;
  // LRM-1067 — hoist description meta so ConversationHeader is not fed a
  // fresh JSX identity every render (React Doctor jsx-no-jsx-as-prop).
  const channelHeaderDescription = active?.description?.trim() ?? "";
  const channelHeaderDescriptionMeta = useMemo(
    () =>
      channelHeaderDescription ? (
        <p
          data-testid="channel-header-description"
          className="truncate text-xs text-muted-foreground"
          title={channelHeaderDescription}
        >
          {channelHeaderDescription}
        </p>
      ) : undefined,
    [channelHeaderDescription],
  );
  const channelConversationPane = (
    <main
      ref={detailHeaderContainerRef}
      className="relative flex flex-1 min-h-0 min-w-0 flex-col bg-background"
    >
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
            layout="slots3"
            leading={
              isMobile ? (
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-10 shrink-0 text-muted-foreground"
                  aria-label={t(($) => $.header.back)}
                  onClick={mobileBackToList}
                >
                  <ArrowLeft className="size-5" />
                </Button>
              ) : active.avatar_url ? (
                // LRM-724 — custom channel icon in the desktop left meta slot.
                <img
                  src={resolvePublicFileUrl(active.avatar_url) ?? undefined}
                  alt=""
                  data-testid="channel-header-meta-tile"
                  className="size-7 shrink-0 rounded-md border border-primary/30 object-cover"
                />
              ) : (
                // LRM-447 slots3 left meta — brand-tinted # tile (design A).
                <span
                  aria-hidden="true"
                  data-testid="channel-header-meta-tile"
                  className="inline-flex size-7 shrink-0 items-center justify-center rounded-md border border-primary/30 bg-primary/10 text-sm font-extrabold text-primary"
                >
                  #
                </span>
              )
            }
            title={
              // LRM-234 — Slack-like desktop title: bold name + tight ▾
              // caret, soft rounded hover/open wash (not primary recolor).
              // LRM-447 — hash moves to left meta slot on desktop; mobile
              // keeps the inline # landmark beside the name.
              <button
                type="button"
                onClick={() => toggleChannelDetails("about")}
                aria-label={t(($) => $.details.open_aria)}
                aria-expanded={channelDetailsOpen && !isMobile}
                title={active.name}
                className={cn(
                  "-ml-1.5 flex min-w-0 flex-1 items-center gap-0.5 rounded-md px-1.5 py-0.5 text-left text-foreground transition-colors",
                  "hover:bg-black/[0.04] dark:hover:bg-white/[0.06]",
                  channelDetailsOpen &&
                    !isMobile &&
                    "bg-black/[0.06] dark:bg-white/[0.08]",
                )}
              >
                {isMobile ? <ChannelHashLandmark size="lg" avatarUrl={active.avatar_url} /> : null}
                <span className="min-w-0 flex-1 truncate font-bold tracking-tight">
                  {active.name}
                </span>
                {!isMobile && (
                  <ChevronDown
                    data-testid="channel-title-chevron"
                    strokeWidth={2.5}
                    className={cn(
                      "size-3 shrink-0 opacity-50 transition-transform duration-150",
                      channelDetailsOpen && "rotate-180 opacity-70",
                    )}
                    aria-hidden="true"
                  />
                )}
              </button>
            }
            // LRM-1067 — channel description under the name; empty = no row / no placeholder.
            meta={channelHeaderDescriptionMeta}
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
            actions={
              // LRM-581 A v3 — Presence Cluster on desktop + mobile (≥32px).
              // Outer Stop chrome removed; Stop lives only inside Working list.
              <div
                className="flex items-center gap-0.5"
                data-testid="channel-header-action-rail"
              >
                <ChannelPresenceCluster
                  members={channelMembers}
                  memberCount={rosterSummary.memberCount}
                  agentCount={rosterSummary.agentCount}
                  tasks={activeTasks}
                  stoppingTaskId={stoppingChannelTaskId}
                  canStop={canPostInChannel}
                  onStopTask={handleStopChannelTask}
                  onStopAll={openStopAllAgentsConfirm}
                  // #821 — the facepile navigates to the Details Members
                  // sub-page (the single roster home), not a separate dialog.
                  onOpenMembers={() => openChannelDetails("members")}
                />
                {isMobile || isHeaderActionsCompact ? (
                  <Button
                    variant="ghost"
                    size="icon"
                    className={cn(
                      "shrink-0 text-muted-foreground",
                      isMobile ? "size-10" : "size-8",
                    )}
                    aria-label={t(($) => $.header.more_aria)}
                    onClick={() => openChannelDetails("about")}
                    data-testid="channel-header-more"
                  >
                    <MoreHorizontal className="size-5" />
                  </Button>
                ) : (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7 rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
                    aria-label={t(($) => $.conv_search.search_aria)}
                    onClick={() => setConvSearchOpen(true)}
                  >
                    <Search className="size-3.5" />
                  </Button>
                )}
              </div>
            }
          />
          <Suspense fallback={null}>
            <ChannelGoalCard
              key={active.id}
              channelId={active.id}
              canManage={canArchive(active)}
              archived={isActiveArchived}
            />
          </Suspense>
              {/* #562 — channel main-content tab switch: Chat (message list),
                  Tasks (channel-scoped board), and LRM-675 Files (channel
                  attachments), full-width in the main area. Uses the shared
                  Tabs primitive so tablist/tab/tabpanel ARIA roles and
                  arrow-key navigation come for free; extend by adding a
                  sibling TabsTrigger + TabsContent for a new view. */}
              <Tabs
                value={channelView}
                onValueChange={(value) =>
                  setChannelView(value as "chat" | "tasks" | "files")
                }
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
                    <TabsTrigger value="files" className="flex-none px-3 py-2">
                      {t(($) => $.view_tabs.files)}
                    </TabsTrigger>
                  </TabsList>
                </div>
                <TabsContent value="files" className="flex flex-1 min-h-0 flex-col text-base">
                  {/* LRM-675 — the single Files entry (settings Files block and
                      duplicate entries removed); same attachment source as the
                      legacy details panel.
                      LRM-1264 R3: mount only while active — inactive TabsContent
                      stays hidden but would otherwise keep queries/DOM. */}
                  {channelView === "files" ? (
                    <Suspense fallback={<ChannelLazyPanelFallback />}>
                      <ChannelFilesPanel channelId={active.id} wide />
                    </Suspense>
                  ) : null}
                </TabsContent>
                <TabsContent value="tasks" className="flex flex-1 min-h-0 flex-col text-base">
                  {channelView === "tasks" ? (
                    <Suspense fallback={<ChannelLazyPanelFallback />}>
                      <ChannelTasksBoard channelId={active.id} />
                    </Suspense>
                  ) : null}
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
                    onChange={(e) => {
                      const next = e.target.value;
                      setConvSearchQuery(next);
                      if (!next.trim()) {
                        setConvSearchResults([]);
                        setConvSearchTotal(0);
                        setConvSearchIndex(0);
                      }
                    }}
                    onKeyDown={(e) => {
                      handleConvSearchInputKeyDown(e, {
                        total: convSearchTotal,
                        onClose: () => {
                          setConvSearchOpen(false);
                          setConvSearchQuery("");
                          setConvSearchResults([]);
                          setConvSearchTotal(0);
                          setConvSearchIndex(0);
                        },
                        // Newest-first: next = older hits, prev = newer hits.
                        onNext: () =>
                          setConvSearchIndex((i) =>
                            convSearchTotal === 0
                              ? 0
                              : Math.min(convSearchTotal - 1, i + 1),
                          ),
                        onPrev: () => setConvSearchIndex((i) => Math.max(0, i - 1)),
                      });
                    }}
                    placeholder={t(($) => $.conv_search.group_placeholder)}
                    className="h-8 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                  />
                  {convSearchQuery.trim() && (
                    <span
                      data-testid="conv-search-count"
                      className="shrink-0 text-xs text-muted-foreground"
                    >
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
                      disabled={convSearchTotal === 0 || convSearchIndex === 0}
                      aria-label={t(($) => $.conv_search.prev_aria)}
                      onClick={() => setConvSearchIndex((i) => Math.max(0, i - 1))}
                    >
                      <ChevronUp className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      disabled={
                        convSearchTotal === 0 ||
                        convSearchIndex >= convSearchTotal - 1
                      }
                      aria-label={t(($) => $.conv_search.next_aria)}
                      onClick={() =>
                        setConvSearchIndex((i) =>
                          Math.min(convSearchTotal - 1, i + 1),
                        )
                      }
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

              <div ref={channelMessageAreaRef} className="contents">
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
                onRetrySend={isActiveArchived ? undefined : handleRetrySend}
                onOpenAgent={handleOpenAgentPanel}
                onOpenMember={handleOpenMemberPanel}
              />
              </div>
              {channelSelectionMenu.menu}

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
                          onClick={() => showErrorToast(t(($) => $.sidebar.restore_permission))}
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
                  {/* LRM-447 — Header owns Stop. Composer strip keeps human
                      typing only; preparing + Stop all rail is gone. */}
                  <ConversationActivityStrip typingActors={activeTypingActors} />
                  <Composer
                    surface="channel"
                    sendLabel={t(($) => $.composer.send)}
                    sendDisabled={
                      (activeDraftEmpty && channelPending.readyAttachmentParts.length === 0) ||
                      channelPending.hasUploading
                    }
                    sending={sendMessage.isPending}
                    onSend={handleSend}
                    voiceChannelId={active.id}
                    voicePlaybackScope={voicePlaybackScope(active.id)}
                    // #838 — an unsent recording must be retried or deleted first;
                    // a new one must never silently replace it. #858 — the shell
                    // derives BOTH the disabled state and the sentence from these,
                    // so a cause can never be blocked without being explained.
                    voiceBlock={{
                      pendingVoice: !!channelPendingVoiceHere,
                      hasTextDraft: !activeDraftEmpty,
                      hasAttachmentDraft: channelPending.pending.length > 0,
                    }}
                    onVoiceSend={handleVoiceSend}
                    isMobile={isMobile}
                    // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- Composer prefix slot; identity is not memo-sensitive
                    prefix={channelSendError || quoteTarget || channelPendingVoiceHere ? (
                      <>
                        <ComposerSendErrorBar
                          error={channelSendError}
                          onRetry={handleSend}
                          onRestore={handleRestorePrevious}
                        />
                        <ComposerPendingVoice
                          pending={channelPendingVoiceHere}
                          retrying={
                            channelPendingVoiceHere !== null &&
                            retryingVoiceTargetId === channelPendingVoiceHere.targetId
                          }
                          onRetry={retryChannelVoice}
                          onDelete={() =>
                            channelPendingVoiceHere &&
                            forgetPendingVoice(channelPendingVoiceHere.targetId)
                          }
                        />
                        {quoteTarget ? (
                          <ComposerQuotePreview
                            quote={quoteTarget}
                            onCancel={() => setQuoteTarget(null)}
                            cancelLabel={t(($) => $.quote.cancel)}
                          />
                        ) : null}
                      </>
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
                        key={`${active.id}:${channelRestoreNonce}`}
                        ref={editorRef}
                        // Chat composer: typed/loaded bare URLs stay plain text
                        // (#531/#542) — made clickable on the read side, not here.
                        plainUrls
                        defaultValue={activeDraft}
                        // LRM-491: Slack-style one-line "Message #channel" (no
                        // @agent tutorial copy). Name is interpolated so the
                        // empty state stays a single short line.
                        placeholder={t(($) => $.composer.placeholder, {
                          name: active.name,
                        })}
                        className={isMobile ? "text-[15px] leading-5" : undefined}
                        onUpdate={handleEditorUpdate}
                        debounceMs={0}
                        onSubmit={handleSend}
                        mediaMode="external"
                        onExternalFiles={channelPending.addFiles}
                        submitOnEnter
                        showBubbleMenu={false}
                        enableChannelReferences
                        mentionAllowedActorIds={mentionAllowedActorIds}
                        scopedMentionAgents={channelAgentCandidates}
                        mentionChannelMemberIds={
                          active?.kind === "group" ? channelMemberIds : null
                        }
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
                        {/* #576 — ProjectPicker moved to group settings. */}
                        {/* LRM-205 — drop composer # issue-ref button (keep typing `#`). */}
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
  // Desktop detail (LRM-400 + LRM-481): flex row — conversation always
  // flex-1 full width (no lone ResizablePanel / persisted %-layout blank
  // half). Side dock is an optional pixel-width column with left-edge drag
  // (360–640, default 520 for Thread/details; persists separately from the
  // global overlay's 520). Opening/closing the dock does not remount the
  // conversation tree. Mobile: no drag — full-screen profile/page route instead.
  const desktopSidePanel = threadPanel ?? agentPanel ?? memberPanel ?? detailsPanel;
  const detailPane = !isMobile ? (
    <div className="flex min-h-0 min-w-0 flex-1" data-testid="channel-detail-row">
      <div
        className="flex min-h-0 min-w-0 flex-1 flex-col"
        data-testid="channel-conversation-column"
      >
        {channelConversationPane}
      </div>
      {notifyPrefsDialog}
      {desktopSidePanel ? (
        <div
          data-testid={
            threadPanel
              ? "thread-side-slot"
              : agentPanel
                ? "agent-side-slot"
                : memberPanel
                  ? "member-side-slot"
                  : "channel-details-side-slot"
          }
          className="relative flex shrink-0 flex-col border-l border-border/30 bg-background"
          style={{ width: detailSideWidth }}
        >
          <button
            type="button"
            data-testid="channel-detail-side-resize"
            aria-label={t(($) => $.details.resize_side_aria)}
            className="absolute inset-y-0 left-0 z-10 w-1.5 cursor-col-resize border-0 bg-transparent p-0 hover:bg-foreground/10"
            onPointerDown={onDetailSideResizePointerDown}
          />
          {/* LRM-877 / LRM-836 — 200ms fade on stack push/pop; motion-reduce instant */}
          <MotionContent
            motionKey={
              threadPanel
                ? `thread:${openThreadRoot?.id ?? "x"}`
                : agentPanel
                  ? `agent:${selectedAgentPanelId}`
                  : memberPanel
                    ? `member:${selectedMemberPanelId}`
                    : `details:${channelDetailsTab}`
            }
            tier="moderate"
            className="flex min-h-0 min-w-0 flex-1 flex-col"
          >
            {desktopSidePanel}
          </MotionContent>
        </div>
      ) : null}
    </div>
  ) : (
    // LRM-877 — mobile Sheet stack: Agent/Member replace the conversation
    // surface (same as DM), so Created Agents → Agent keeps a pop path.
    threadPanel ?? agentPanel ?? memberPanel ?? channelConversationPane
  );

  // DM detail pane — rendered in place of the group detail when a DM is active.
  // Visible direct messages use the R2 dm_channel surface; legacy sessions are
  // fail-closed inside DmConversation until the backend source is removed.
  // When a `?dm=` deep link opens cold, `activeDmId` is set before the DM list
  // resolves the row — keep the conversation structure in place instead of
  // dropping to a blank pane during that window.
  const dmDraftKey = activeDm ? (`dm:${activeDm.id}` as const) : null;
  const dmDetailPane = activeDm ? (
    <Suspense fallback={<ChannelLazyPanelFallback />}>
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
        // Same Reminder-anchor deep-link values the group-channel path above
        // consumes — mutually exclusive in practice (a resolved route is
        // either activeChannelId or activeDmId, never both), so it's safe to
        // pass through unconditionally; DmConversation owns its own one-shot
        // consumption guard.
        threadDeepLinkId={threadDeepLinkId}
        deepLinkMessageId={highlightMessageId}
      />
    </Suspense>
  ) : dmResolveTimedOut ? (
    <DmOpenFailedState
      onRetry={() => {
        setDmResolveTimedOut(false);
        void refetchDms();
      }}
    />
  ) : (
    <ConversationSwitchSkeleton isMobile={isMobile} />
  );

  // The detail surface: a selected DM wins over a group (selections are
  // mutually exclusive, but this also covers the deep-link-before-list-loads
  // window where `activeDmId` is set but the DM row hasn't resolved yet).
  //
  // Activity embed (LRM-388 / LRM-400): pin to thread-only or channel-stream-only
  // so the Activity right pane never mounts the desktop dual-pane (blank half +
  // stranded Stop all). DMs keep the full DmConversation shell.
  // LRM-740 — Activity embed `embeddedSurface=thread` must still host the
  // Agent/Member dock when a Thread avatar is clicked. Previously only
  // `threadPanel` was mounted, so opening Profile cleared Thread and fell
  // through to the skeleton (silent no-op).
  const detailSurface = activeDmId
    ? dmDetailPane
    : embedded
      ? embeddedSurface === "thread"
        ? (threadPanel ??
            agentPanel ??
            memberPanel ?? (
              <ConversationSwitchSkeleton isMobile={isMobile} />
            ))
        : channelConversationPane
      : detailPane;

  // Embedded + resolved route id but channel/DM missing from lists → explicit
  // error (no silent swap to another conversation — LRM-238).
  const embeddedMissingTarget =
    embedded &&
    !!channelId &&
    channelsLoaded &&
    !active &&
    !activeDm &&
    reconciledRouteIdRef.current === channelId;

  if (!viewportReady) {
    return <InitialChannelsShellSkeleton />;
  }

  if (embedded) {
    return (
      <AgentPanelProvider onOpenAgent={handleOpenAgentPanel}>
        <MemberPanelProvider onOpenMember={handleOpenMemberPanel}>
          <div
            className="flex h-full min-h-0 flex-col"
            data-testid="channels-page-embedded"
            data-embedded-surface={embeddedSurface ?? "auto"}
          >
            {embeddedMissingTarget ? (
              <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
                <p className="text-sm text-destructive">
                  {t(($) => $.thread.view_in_channel_failed)}
                </p>
              </div>
            ) : (
              detailSurface
            )}
          </div>
        </MemberPanelProvider>
      </AgentPanelProvider>
    );
  }

  return (
    <AgentPanelProvider onOpenAgent={handleOpenAgentPanel}>
    <MemberPanelProvider onOpenMember={handleOpenMemberPanel}>
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
        // react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop
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

      {/* Mobile / narrow (#568) — LRM-494: full-page Slack channel details
          (not a flat bottom-sheet menu). Selecting ⋯ or the header title
          opens ChannelDetailsPanel overview; drill-downs stay inside. */}
      {(isMobile || isHeaderActionsCompact) && active && detailsPanelProps && (
        <Drawer
          direction="bottom"
          open={mobilePanel !== null}
          onOpenChange={(open) => {
            if (!open) setMobilePanel(null);
          }}
        >
          <DrawerContent
            className="flex h-[100dvh] max-h-[100dvh] flex-col gap-0 overflow-hidden rounded-none p-0"
            data-testid="channel-details-page-drawer"
          >
            {mobilePanel ? (
              <Suspense fallback={<ChannelLazyPanelFallback className="flex flex-1 min-h-0 flex-col gap-2 p-4" />}>
              <ChannelDetailsPanel
                key={`${active.id}:${mobilePanel}`}
                {...detailsPanelProps}
                initialTab={mobilePanel}
                variant="page"
                onClose={() => setMobilePanel(null)}
                portalContainer={mobileSettingsDrawerBodyRef}
              />
              </Suspense>
            ) : null}
          </DrawerContent>
        </Drawer>
      )}

      {/* #821 — the standalone members dialog is retired; the Details Members
          sub-page (memberPanelBody) is the single roster home. */}
      {active && !isActiveSystemChannel && (
        <ChannelAddPeopleDialog
          open={addPeopleDialogOpen}
          onOpenChange={(open) => {
            setAddPeopleDialogOpen(open);
            if (!open) {
              setInviteQuery("");
              setDebouncedInviteQuery("");
              setSelectedInvites(new Set());
            }
          }}
          channelName={active.name}
          candidates={inviteCandidates}
          allCandidates={allInviteCandidates}
          loading={inviteCandidatesLoading}
          error={inviteCandidatesErrorFlag}
          onRetry={retryInviteCandidates}
          query={inviteQuery}
          onQueryChange={setInviteQuery}
          selected={selectedInvites}
          onToggle={toggleInvite}
          onClearOne={(key) =>
            setSelectedInvites((prev) => {
              const next = new Set(prev);
              next.delete(key);
              return next;
            })
          }
          onSubmit={inviteSelected}
          submitting={addMembers.isPending}
        />
      )}

      {/* #832 — ownership transfer asks first. Not because it is irreversible
          (the new owner can transfer back) but because the viewer loses their
          own owner rights the moment it succeeds — the confirmation exists so
          they know precisely what they are giving up (Iris). */}
      <Sheet
        open={transferTarget !== null}
        onOpenChange={(open) => {
          if (!open) setTransferTarget(null);
        }}
      >
        <SheetContent side="bottom" showCloseButton={false} className="gap-0 rounded-t-2xl p-0">
          <SheetHeader className="space-y-1 p-4 pb-2 text-left">
            <SheetTitle>{t(($) => $.members.menu.role_transfer_title)}</SheetTitle>
            <SheetDescription>
              {t(($) => $.members.menu.role_transfer_body, {
                name: transferTarget
                  ? resolveActorDisplayName(
                      transferTarget,
                      transferTarget.member_type === "agent"
                        ? t(($) => $.message.agent_badge)
                        : t(($) => $.members.title),
                    )
                  : "",
              })}
            </SheetDescription>
          </SheetHeader>
          <SheetFooter className="gap-2 p-4 pt-2">
            <Button
              type="button"
              className="min-h-11 w-full"
              data-testid="group-member-transfer-confirm"
              onClick={() => {
                const target = transferTarget;
                setTransferTarget(null);
                if (target) runRoleChange(target, "transfer");
              }}
            >
              {t(($) => $.members.menu.role_transfer_confirm)}
            </Button>
            <Button
              type="button"
              variant="outline"
              className="min-h-11 w-full"
              onClick={() => setTransferTarget(null)}
            >
              {t(($) => $.members.remove_cancel)}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* LRM-1289 width fix + LRM-1327 pending/focus (LRM-1300 A/D3). */}
      <RemoveMemberConfirmDialog
        open={removeMemberTarget !== null}
        displayName={
          removeMemberTarget
            ? resolveActorDisplayName(
                removeMemberTarget,
                removeMemberTarget.member_type === "agent"
                  ? t(($) => $.message.agent_badge)
                  : t(($) => $.members.title),
              )
            : ""
        }
        pending={removeMember.isPending}
        onOpenChange={(open) => {
          if (!open) setRemoveMemberTarget(null);
        }}
        onConfirm={() => {
          if (!active || !removeMemberTarget) return;
          // Capture: success clears the target; failure keeps the dialog open
          // (#833 / LRM-1300 §5) so callbacks must not read state back.
          const target = removeMemberTarget;
          const channelId = active.id;
          removeMember.mutate(
            {
              channelId,
              memberType: target.member_type,
              memberId: target.member_id,
            },
            {
              onError: () => {
                showErrorToast(t(($) => $.members.remove_failed));
                // #839 — row mark survives toast dismiss/expiry.
                setRemoveFailedKeys((prev) =>
                  new Set(prev).add(memberFailureKey(channelId, target)),
                );
              },
              onSuccess: () => {
                setRemoveFailedKeys((prev) => {
                  const next = new Set(prev);
                  next.delete(memberFailureKey(channelId, target));
                  return next;
                });
                setRemoveMemberTarget(null);
              },
            },
          );
        }}
      />

      <DeleteChannelDialog
        open={deleteTarget !== null}
        channelName={deleteTarget?.name ?? ""}
        pending={deleteChannel.isPending}
        onConfirm={handleDelete}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      />

      {active && canPostInChannel ? (
        <StopAllAgentsDialog
          open={stopAllConfirmOpen}
          onOpenChange={setStopAllConfirmOpen}
          channelName={active.name}
          onConfirm={confirmStopAllAgents}
          confirming={isStoppingAllChannelTasks}
        />
      ) : null}

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
    </MemberPanelProvider>
    </AgentPanelProvider>
  );
}
