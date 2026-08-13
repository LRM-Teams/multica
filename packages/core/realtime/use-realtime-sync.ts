"use client";

import { useEffect, useRef } from "react";
import { useQueryClient, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import type { WSClient } from "../api/ws-client";
import type { StoreApi, UseBoundStore } from "zustand";
import type { AuthState } from "../auth/store";
import { createLogger } from "../logger";
import { clearWorkspaceStorage } from "../platform/storage-cleanup";
import { defaultStorage } from "../platform/storage";
import { getCurrentWsId, getCurrentSlug } from "../platform/workspace-storage";
import { issueKeys } from "../issues/queries";
import { projectKeys } from "../projects/queries";
import { pinKeys } from "../pins/queries";
import { runtimeKeys } from "../runtimes/queries";
import { sandboxKeys } from "../sandboxes/queries";
import { labelKeys } from "../labels/queries";
import {
  agentTaskSnapshotKeys,
  agentActivityKeys,
  agentRunCountsKeys,
  agentTasksKeys,
  runnerActivityKeys,
  runnerActivitySummaryKeys,
} from "../agents/queries";
import { patchAgentTaskSnapshotStatus } from "../agents/task-snapshot-updaters";
import { applyRunnerActivityRealtime } from "../agents/runner-activity-updaters";
import { agentPresenceKeys } from "../agents/agent-presence";
import { applyAgentPresenceRealtime } from "../agents/agent-presence-updaters";
import { githubKeys } from "../github/queries";
import { larkKeys } from "../lark/queries";
import {
  onIssueCreated,
  onIssueUpdated,
  onIssueDeleted,
  onIssueLabelsChanged,
  onIssueMetadataChanged,
} from "../issues/ws-updaters";
import { onInboxNew, onInboxInvalidate, onInboxIssueStatusChanged, onInboxIssueDeleted } from "../inbox/ws-updaters";
import { inboxKeys } from "../inbox/queries";
import {
  notificationPreferenceOptions,
  notificationPreferenceKeys,
} from "../notification-preferences/queries";
import { workspaceKeys, workspaceListOptions } from "../workspace/queries";
import { applyMemberPresenceEvent } from "../workspace/use-member-presence";
import type { MemberPresencePayload } from "../types/events";
import {
  buildNotificationRoute,
  showWebNotification,
  type SystemNotificationPayload,
} from "../platform/system-notification";
import type { Workspace } from "../types/workspace";
import { chatKeys } from "../chat/queries";
import { shouldClearChatPendingOnDone } from "../chat/queries";
import { noteKeys } from "../notes/queries";
import {
  channelKeys,
  invalidateChannelIssues,
  invalidateChannelMessages,
  patchChannelMessageReactionInCache,
  upsertChannelMessageInCache,
  upsertChannelMessageThreadInCache,
} from "../channels/queries";
import { channelGoalKeys } from "../channels/goal";
import { messageMentionsViewer } from "../channels/mentions-viewer";
import { dmKeys } from "../dm/queries";
import { conversationKeys } from "../conversations";
import { applyResearchWSEvent } from "../research/ws-updaters";
import type { WSMessage } from "../types/events";
import { voiceCallKeys } from "../voice-calls/queries";
import type { DMItem } from "../dm/types";
import { useChatStore } from "../chat";
import { resolvePostAuthDestination, useHasOnboarded } from "../paths";
import type {
  MemberAddedPayload,
  WorkspaceDeletedPayload,
  WorkspaceUpdatedPayload,
  MemberRemovedPayload,
  IssueUpdatedPayload,
  IssueCreatedPayload,
  IssueDeletedPayload,
  IssueLabelsChangedPayload,
  IssueMetadataChangedPayload,
  InboxNewPayload,
  InboxItem,
  NotificationPreferenceResponse,
  CommentCreatedPayload,
  CommentUpdatedPayload,
  CommentDeletedPayload,
  CommentResolvedPayload,
  CommentUnresolvedPayload,
  ActivityCreatedPayload,
  ReactionAddedPayload,
  ReactionRemovedPayload,
  IssueReactionAddedPayload,
  IssueReactionRemovedPayload,
  SubscriberAddedPayload,
  SubscriberRemovedPayload,
  TaskMessagePayload,
  TaskQueuedPayload,
  TaskDispatchPayload,
  TaskRunningPayload,
  TaskCompletedPayload,
  TaskFailedPayload,
  TaskCancelledPayload,
  AgentTask,
  ChatDonePayload,
  ChatMessage,
  ChatPendingTask,
  ChatMessagesPage,
  Channel,
  ChannelMessage,
  ChannelReaction,
  InvitationCreatedPayload,
  VoiceCallUpdatedPayload,
} from "../types";

const chatWsLogger = createLogger("chat.ws");

const logger = createLogger("realtime-sync");

export function invalidateChatMessageQueries(
  qc: QueryClient,
  sessionId: string,
) {
  qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
  qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
}

export function invalidateVoiceCallFromRealtime(
  qc: QueryClient,
  payload: VoiceCallUpdatedPayload,
) {
  if (!payload.workspace_id || !payload.call_id) return;
  qc.invalidateQueries({
    queryKey: voiceCallKeys.detail(payload.workspace_id, payload.call_id),
  });
}

export function applyChatDoneToCache(
  qc: QueryClient,
  payload: ChatDonePayload,
) {
  const sessionId = payload.chat_session_id;
  const taskId = payload.task_id;
  const messageId = payload.message_id;
  const content = payload.content;
  if (messageId && content !== undefined) {
    const assistant: ChatMessage = {
      id: messageId,
      chat_session_id: sessionId,
      role: "assistant",
      content,
      parts: payload.parts,
      task_id: taskId,
      created_at: payload.created_at ?? new Date().toISOString(),
      elapsed_ms: payload.elapsed_ms ?? null,
    };
    qc.setQueryData<ChatMessage[] | undefined>(
      chatKeys.messages(sessionId),
      (old) => {
        if (!old) return old; // first fetch will pick it up
        // Idempotent against reconnect replay.
        if (old.some((m) => m.id === messageId)) return old;
        return [...old, assistant];
      },
    );
    qc.setQueryData<InfiniteData<ChatMessagesPage> | undefined>(
      chatKeys.messagesPage(sessionId),
      (old) => patchLatestChatMessagePage(old, assistant),
    );
  }
  // Replacement is in the messages list now; safe to drop pending unless a
  // newer follow-up turn has already been queued for the same session.
  qc.setQueryData<ChatPendingTask | Record<string, never>>(
    chatKeys.pendingTask(sessionId),
    () => (shouldClearChatPendingOnDone() ? {} : {}),
  );
  // Authoritative refetch reconciles redaction / migrations / clients
  // that took the fallback branch above.
  invalidateChatMessageQueries(qc, sessionId);
  qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
}

function patchLatestChatMessagePage(
  old: InfiniteData<ChatMessagesPage> | undefined,
  message: ChatMessage,
): InfiniteData<ChatMessagesPage> | undefined {
  if (!old?.pages.length) return old;
  const seen = old.pages.some((page) => page.messages.some((m) => m.id === message.id));
  if (seen) return old;
  return {
    ...old,
    pages: old.pages.map((page, index) => {
      if (index !== 0) return page;
      return {
        ...page,
        messages: [...page.messages, message],
      };
    }),
  };
}

/**
 * Apply a workspace:updated event to the cache. Always refreshes the
 * workspace list. If the incoming `issue_prefix` differs from what's
 * currently cached, also invalidates issueKeys.all for that workspace,
 * since every issue's rendered identifier (`MUL-123`) is recomputed from
 * the workspace prefix at read time. Without this, the UI keeps showing
 * the old `OLD-N` keys until the next hard refresh.
 *
 * If the workspace isn't in the cached list (first observation), we
 * conservatively invalidate — the prefix is effectively "new" relative to
 * what's cached, so any issues already loaded under the old prefix would
 * be stale anyway.
 */
export function applyWorkspaceUpdatedToCache(
  qc: QueryClient,
  payload: WorkspaceUpdatedPayload,
): void {
  const next = payload.workspace;
  if (next?.id) {
    const cached =
      qc
        .getQueryData<Workspace[]>(workspaceKeys.list())
        ?.find((w) => w.id === next.id) ?? null;
    if (!cached || cached.issue_prefix !== next.issue_prefix) {
      qc.invalidateQueries({ queryKey: issueKeys.all(next.id) });
    }
  }
  qc.invalidateQueries({ queryKey: workspaceKeys.list() });
}

/**
 * Resolves the slug of the workspace an inbox item originated from, via the
 * cached workspace list (fetched once when the cache is cold).
 *
 * Desktop notification routing must pin to the *source* workspace of the
 * inbox item, not the currently active one: the user can be on workspace B
 * when an `inbox:new` for workspace A arrives, and macOS Notification Center
 * holds banners across workspace switches. Returns null when the workspace
 * cannot be resolved — callers must NOT fall back to the current slug (that
 * recreates the wrong-workspace routing this exists to prevent, #3766) and
 * should show the notification without a deep link instead.
 */
export async function resolveInboxSourceSlug(
  qc: QueryClient,
  workspaceId: string,
): Promise<string | null> {
  if (!workspaceId) return null;
  try {
    const workspaces = await qc.ensureQueryData(workspaceListOptions());
    return workspaces?.find((w) => w.id === workspaceId)?.slug ?? null;
  } catch {
    // Workspace list unavailable (e.g. network hiccup): degrade to a
    // link-less notification rather than guessing a slug.
    return null;
  }
}

/**
 * Handles an `inbox:new` event end-to-end: inbox cache invalidation, the
 * focus / mute checks, and the native OS banner. Exported so the handler
 * behavior (not just slug resolution) is testable.
 *
 * Every workspace-scoped read here keys on the ITEM's workspace
 * (`item.workspace_id`), never the currently active one (#3766): the cache
 * invalidation must refresh the source workspace's inbox list / unread
 * count / dock badge, the mute check must honor the source workspace's
 * preference, and the deep link must carry the source workspace's slug.
 */
async function isSystemNotificationMuted(
  qc: QueryClient,
  sourceWsId: string,
  slug: string | null,
): Promise<boolean> {
  if (!sourceWsId) return false;
  try {
    const prefData = slug
      ? await qc.ensureQueryData(
          notificationPreferenceOptions(sourceWsId, slug),
        )
      : qc.getQueryData<NotificationPreferenceResponse>(
          notificationPreferenceKeys.all(sourceWsId),
        );
    return prefData?.preferences?.system_notifications === "muted";
  } catch {
    // Fall through with default behavior.
    return false;
  }
}

async function deliverSystemNotification(
  qc: QueryClient,
  sourceWsId: string,
  payload: Omit<SystemNotificationPayload, "slug">,
): Promise<void> {
  if (typeof document !== "undefined" && document.hasFocus()) return;
  const slug = await resolveInboxSourceSlug(qc, sourceWsId);
  if (await isSystemNotificationMuted(qc, sourceWsId, slug)) return;
  const fullPayload: SystemNotificationPayload = { ...payload, slug: slug ?? "" };
  fullPayload.url = buildSystemNotificationUrl(fullPayload);
  const desktopAPI = (
    globalThis as unknown as {
      desktopAPI?: {
        showNotification?: (payload: SystemNotificationPayload) => void;
      };
    }
  ).desktopAPI;
  if (desktopAPI?.showNotification) {
    desktopAPI.showNotification(fullPayload);
    return;
  }
  showWebNotification(fullPayload);
}

function buildSystemNotificationUrl(payload: SystemNotificationPayload): string {
  return buildNotificationRoute(payload) ?? "/";
}

export async function handleInboxNew(
  qc: QueryClient,
  item: InboxItem,
): Promise<void> {
  const sourceWsId = item.workspace_id;
  if (sourceWsId) onInboxNew(qc, sourceWsId, item);
  // `issueKey` matches the inbox page's URL selector (issue id when the
  // item is attached to an issue, otherwise the inbox item id). `itemId`
  // is the inbox row's own id, needed to fire markInboxRead on click.
  // A null slug (workspace list unavailable / item from a workspace this
  // client can't see) still shows the banner — the user should learn about
  // the inbox item — but with an empty slug so the click is a no-op
  // (the inbox bridge ignores empty slugs) instead of routing wrong.
  await deliverSystemNotification(qc, sourceWsId, {
    itemId: item.id,
    issueKey: item.issue_id ?? item.id,
    title: item.title,
    body: item.body ?? "",
  });
}

function notificationBody(author: string, content: string): string {
  const normalized = content.replace(/\s+/g, " ").trim();
  const text = normalized.length > 120 ? `${normalized.slice(0, 117)}...` : normalized;
  return text ? `${author}: ${text}` : author;
}

/**
 * LRM-411 / LRM-414 channel+DM system-notification gate (client WS path).
 *
 * Locked product rules:
 * - Unmuted group/DM: every new message may notify (when authorized / unfocused).
 * - Muted: suppress ambient messages; still notify on @me / @all.
 * - Self-sent user messages never self-notify.
 * - `type=system` channel events never notify (LRM-414).
 * - Issue assignment still notifies via `inbox:new` → `handleInboxNew` (not gated here).
 */
export function shouldNotifyChannelMessage(
  message: ChannelMessage,
  opts: {
    myUserId?: string;
    channelMuted?: boolean;
    mentionsViewer?: boolean;
  },
): boolean {
  if (message.edited_at || message.deleted_at) return false;
  if (message.type === "system") return false;
  if (message.type === "user" && message.author_id === opts.myUserId) return false;

  const mentionsViewer =
    opts.mentionsViewer ??
    messageMentionsViewer(message.content, opts.myUserId, message.parts);

  // Mute suppresses ambient traffic only; @me / @all still ring (LRM-411).
  if (opts.channelMuted && !mentionsViewer) return false;

  return true;
}

export async function handleChannelMessageNotification(
  qc: QueryClient,
  message: ChannelMessage,
  myUserId?: string,
): Promise<void> {
  const sourceWsId = message.workspace_id;
  if (!sourceWsId) return;

  const channels = qc.getQueryData<Channel[]>(channelKeys.list(sourceWsId)) ?? [];
  const channel = channels.find((c) => c.id === message.channel_id) ?? null;
  const dmItems = qc.getQueryData<DMItem[]>(dmKeys.list(sourceWsId)) ?? [];
  const dmItem = dmItems.find((d) => d.id === message.channel_id) ?? null;
  const isDM = channel?.kind === "dm" || dmItem != null;
  const channelMuted = !!(
    channel?.muted ||
    channel?.muted_at ||
    dmItem?.muted ||
    dmItem?.muted_at
  );
  const mentionsViewer = messageMentionsViewer(
    message.content,
    myUserId,
    message.parts,
  );

  if (
    !shouldNotifyChannelMessage(message, {
      myUserId,
      channelMuted,
      mentionsViewer,
    })
  ) {
    return;
  }

  const title = isDM
    ? message.author_name || dmItem?.peer?.name || "Direct message"
    : channel?.name
      ? `#${channel.name}`
      : "New group message";

  await deliverSystemNotification(qc, sourceWsId, {
    itemId: message.id,
    channelId: isDM ? undefined : message.channel_id,
    dmId: isDM ? message.channel_id : undefined,
    threadId: message.thread_root_message_id ?? undefined,
    title,
    body: notificationBody(message.author_name || "Someone", message.content),
  });
}

/**
 * Invalidates all workspace-scoped queries. Used after reconnect and when a
 * new WSClient instance is detected (workspace switch) to recover events
 * missed while disconnected.
 */
function invalidateSessionScopedChatQueries(qc: QueryClient): void {
  qc.invalidateQueries({
    predicate: (query) => {
      const key = query.queryKey;
      return (
        (key[0] === "chat" &&
          (key[1] === "messages" ||
            key[1] === "messages-page" ||
            key[1] === "pending-task")) ||
        key[0] === "task-messages"
      );
    },
  });
}

function invalidateWorkspaceScopedQueries(qc: QueryClient): void {
  const wsId = getCurrentWsId();
  if (wsId) {
    qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: inboxKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
    qc.invalidateQueries({ queryKey: workspaceKeys.invitations(wsId) });
    qc.invalidateQueries({ queryKey: projectKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: agentActivityKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: agentRunCountsKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: runnerActivitySummaryKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: runnerActivityKeys.root(wsId) });
    qc.invalidateQueries({ queryKey: agentPresenceKeys.workspace(wsId) });
    qc.invalidateQueries({ queryKey: chatKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: labelKeys.all(wsId) });
    qc.invalidateQueries({ queryKey: voiceCallKeys.all(wsId) });
  }
  // Per-issue caches are keyed without wsId, so the issueKeys.all(wsId)
  // prefix above does not reach them. They rely entirely on WS events for
  // freshness (staleTime: Infinity), so events missed while disconnected
  // left them stale until a full reload — the inbox showed an agent's new
  // comment while the issue timeline didn't (#3953). Inactive caches only
  // get marked stale here and refetch on next mount; the one mounted issue
  // refetches immediately, same as its own useWSReconnect already does.
  qc.invalidateQueries({ queryKey: issueKeys.timelineAll() });
  qc.invalidateQueries({ queryKey: issueKeys.reactionsAll() });
  qc.invalidateQueries({ queryKey: issueKeys.subscribersAll() });
  qc.invalidateQueries({ queryKey: issueKeys.usageAll() });
  qc.invalidateQueries({ queryKey: issueKeys.attachmentsAll() });
  qc.invalidateQueries({ queryKey: issueKeys.tasksAll() });
  // The channel Tasks board (#562) is keyed by channelId, not wsId, so the
  // workspace-scoped invalidations above don't reach it; refetch it on
  // reconnect so it recovers task changes missed while disconnected.
  invalidateChannelIssues(qc);
  // Active chat session queries are keyed by session/task id instead of wsId.
  // Without this, a WS reconnect after server restart can leave the chat UI
  // stuck on a stale queued pill until a full page refresh.
  invalidateSessionScopedChatQueries(qc);
  qc.invalidateQueries({ queryKey: workspaceKeys.list() });
}

export interface RealtimeSyncStores {
  authStore: UseBoundStore<StoreApi<AuthState>>;
}

/**
 * Centralized WS -> store sync. Called once from WSProvider.
 *
 * Uses invalidation by default, with dedicated cache patches for authoritative
 * realtime projections:
 * - onAny handler extracts event prefix and invalidates matching Query caches
 * - Debounce per-prefix prevents rapid-fire refetches (e.g. bulk issue updates)
 * - Precise handlers patch complete/delta payloads or perform side effects
 *
 * Per-issue events (comments, activity, reactions, subscribers) are handled
 * both here (invalidation fallback) and by per-page useWSEvent hooks (granular
 * updates). Daemon register events invalidate runtimes globally; heartbeats
 * are skipped to avoid excessive refetches.
 *
 * @param ws - WebSocket client instance (null when not yet connected)
 * @param stores - Platform-created Zustand store instances for auth and workspace
 * @param onToast - Optional callback for showing toast messages (platform-specific)
 */
export function useRealtimeSync(
  ws: WSClient | null,
  stores: RealtimeSyncStores,
  onToast?: (message: string, type?: "info" | "error") => void,
) {
  const { authStore } = stores;
  const qc = useQueryClient();

  // Captured via ref so the (rare) hasOnboarded change doesn't re-subscribe
  // every WS handler in this effect. The resolver reads `.current` at the
  // moment workspace-loss fires, which is what we want.
  const hasOnboarded = useHasOnboarded();
  const hasOnboardedRef = useRef(hasOnboarded);
  hasOnboardedRef.current = hasOnboarded;

  // Main sync: onAny -> refreshMap with debounce
  useEffect(() => {
    if (!ws) return;
    // Bind Presence events to the Workspace that owned this WS subscription.
    // A late event from a client being torn down during Workspace switching
    // must never be applied to the newly selected Workspace cache.
    const presenceWorkspaceId = getCurrentWsId() ?? undefined;

    const refreshMap: Record<string, () => void> = {
      inbox: () => {
        const wsId = getCurrentWsId();
        if (wsId) onInboxInvalidate(qc, wsId);
      },
      agent: () => {
        const wsId = getCurrentWsId();
        if (wsId) {
          qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
        }
      },
      member: () => {
        const wsId = getCurrentWsId();
        if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.members(wsId) });
      },
      // workspace:updated is handled by the specific handler below
      // (compares prefixes to decide whether to also invalidate issues).
      // This generic fallback still fires for workspace:deleted (paired
      // with the specific navigation handler) and any future workspace:*
      // events without dedicated handlers.
      workspace: () => {
        qc.invalidateQueries({ queryKey: workspaceKeys.list() });
      },
      skill: () => {
        const wsId = getCurrentWsId();
        if (wsId) qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
      },
      project: () => {
        const wsId = getCurrentWsId();
        if (wsId) qc.invalidateQueries({ queryKey: projectKeys.all(wsId) });
      },
      label: () => {
        // label:created/updated/deleted — also refresh issues, since each
        // issue carries a denormalized snapshot of its labels (rename/recolor
        // /delete on a label needs to flush the chips on every issue showing
        // it).
        const wsId = getCurrentWsId();
        if (wsId) {
          qc.invalidateQueries({ queryKey: ["labels", wsId] });
          qc.invalidateQueries({ queryKey: issueKeys.all(wsId) });
        }
      },
      pin: () => {
        const wsId = getCurrentWsId();
        const userId = authStore.getState().user?.id;
        if (wsId && userId) qc.invalidateQueries({ queryKey: pinKeys.all(wsId, userId) });
      },
      daemon: () => {
        const wsId = getCurrentWsId();
        if (wsId) {
          qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
        }
      },
      computer: () => {
        const wsId = getCurrentWsId();
        if (wsId) {
          qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
        }
      },
      sandbox: () => {
        const wsId = getCurrentWsId();
        if (wsId) qc.invalidateQueries({ queryKey: sandboxKeys.all(wsId) });
      },
      github_installation: () => {
        const wsId = getCurrentWsId();
        if (wsId) qc.invalidateQueries({ queryKey: githubKeys.installations(wsId) });
      },
      lark_installation: () => {
        const wsId = getCurrentWsId();
        if (wsId) qc.invalidateQueries({ queryKey: larkKeys.installations(wsId) });
      },
      pull_request: () => {
        // PR list is keyed by issue id, not workspace, so we invalidate all
        // PR queries — the open issue detail page will refetch its own list.
        qc.invalidateQueries({ queryKey: ["github", "pull-requests"] });
      },
      // Task lifecycle changes update Workload/task projections only.
      // Agent Presence is patched exclusively by `agent:presence` below.
      task: () => {
        const wsId = getCurrentWsId();
        if (!wsId) return;
        // NOTE (step②): the agent-task-snapshot is NO LONGER invalidated here.
        // A whole-workspace refetch on every task event (×every client) was the
        // p95 amplifier. Its status is now patched in place from the payload by
        // the task lifecycle ws.on handlers below (patchAgentTaskSnapshotStatus),
        // with a single coalesced refetch only when a brand-new task appears.
        // NOTE (step② #2/#3): the 30d activity histogram and run-count series
        // are NO LONGER invalidated per task event. They are 30-DAY aggregates
        // where a single completion shifts a bucket by one — imperceptible — so
        // refetching the whole series on every lifecycle event (×every client)
        // was pure load on the `activity?tab=all` path for no visible gain. They
        // now refresh via their own 60s staleTime + refetchOnWindowFocus
        // (bounded resync); a just-completed run shows in the histogram within
        // that window or on focus, which is the correct latency budget for a
        // 30-day chart. This directly cuts the per-event refetch pressure on the
        // activity query (the p95 target).
        // Per-agent task list (Activity tab "Recent work"). Prefix match
        // catches every agent's list — the per-agent detail key sits
        // under agentTasks/<wsId>/<agentId>.
        qc.invalidateQueries({ queryKey: agentTasksKeys.all(wsId) });
        // Per-issue task list (issue-detail Execution log). Prefix match
        // across all issues — keeps the contract "any task: event makes
        // every list-of-tasks query stale" so cache stays fresh even
        // when the relevant component isn't currently mounted.
        qc.invalidateQueries({ queryKey: ["issues", "tasks"] });
        // Per-issue token usage card (issue-detail right rail). Same
        // shape as the tasks invalidation above — any task lifecycle
        // event shifts the aggregated usage numbers.
        qc.invalidateQueries({ queryKey: ["issues", "usage"] });
        // Comment trigger previews answer "who would a send wake right
        // now" — the pending-task dedup guard makes that answer
        // queue-dependent, so any task lifecycle change must refresh an
        // open composer's chips (e.g. an agent finishing its run becomes
        // triggerable again mid-typing).
        qc.invalidateQueries({ queryKey: issueKeys.commentTriggerPreviewAll() });
      },
    };

    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    // Per-prefix debounce windows. The `task` prefix fans out the heaviest
    // refresh — a whole-workspace agent-task-snapshot refetch plus activity /
    // run-count / squad-status / trigger-preview invalidations — on EVERY task
    // lifecycle event. With many concurrent agents (and every connected client
    // doing the same), a 100ms window still let sustained task activity storm
    // those endpoints (agent-task-snapshot ran ~2.7s under that load). Coalesce
    // `task` over a longer window so a burst collapses to one refetch. Other
    // prefixes stay snappy at 100ms.
    // (Immediate mitigation; the durable fix is WS-delta in-place snapshot
    // updates instead of event→full-refetch.)
    // `task-snapshot-newrow`: step② coalesces the single snapshot refetch that
    // a brand-new (not-yet-cached) task requires, so a burst of new tasks
    // collapses to ONE refetch instead of one per event (Parker's guardrail:
    // never trade one whole-table refetch for fifty single fetches).
    const DEBOUNCE_MS_BY_PREFIX: Record<string, number> = {
      task: 2000,
      "task-snapshot-newrow": 500,
    };
    const debouncedRefresh = (prefix: string, fn: () => void) => {
      const existing = timers.get(prefix);
      if (existing) clearTimeout(existing);
      timers.set(
        prefix,
        setTimeout(() => {
          timers.delete(prefix);
          fn();
        }, prefix.startsWith("work-graph:") ? 300 : (DEBOUNCE_MS_BY_PREFIX[prefix] ?? 100)),
      );
    };

    // step②: patch the agent-task-snapshot in place from a task lifecycle
    // event. Returns nothing; when the task isn't cached yet (a brand-new task
    // whose full AgentTask row the event payload can't supply) it coalesces one
    // snapshot refetch per burst instead of the old per-event full refetch.
    const applyTaskStatusToSnapshot = (
      taskId: string,
      status: AgentTask["status"],
    ) => {
      const wsId = getCurrentWsId();
      if (!wsId) return;
      if (!patchAgentTaskSnapshotStatus(qc, wsId, taskId, status)) {
        debouncedRefresh("task-snapshot-newrow", () =>
          qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.list(wsId) }),
        );
      }
    };

    // Event types handled by specific handlers below -- skip generic refresh
    const specificEvents = new Set([
      "workspace:updated",
      "issue:updated", "issue:created", "issue:deleted", "issue_labels:changed", "issue_metadata:changed", "inbox:new",
      "comment:created", "comment:updated", "comment:deleted",
      "comment:resolved", "comment:unresolved",
      "activity:created",
      // Full server projection: patch the Query cache below. Reconnect is the
      // bounded REST reconciliation path; normal events must not fan out into
      // agents, fleet rankings, or runner-activity refetches.
      "agent:activity",
      "agent:presence",
      "reaction:added", "reaction:removed",
      "issue_reaction:added", "issue_reaction:removed",
      "subscriber:added", "subscriber:removed",
      "daemon:heartbeat",
      // LRM-462: patched into member-presence cache below; do not invalidate members list.
      "member:presence",
      // Chat events are handled explicitly below; do not double-invalidate.
      "chat:message", "chat:done", "chat:session_read", "chat:session_deleted",
      "chat:session_updated",
      "voice_call:updated",
      // task:message stays out of the prefix path because it fires per
      // streamed message during a long run — invalidating the snapshot on
      // every message would flood the network. Specific chat handlers below
      // still receive it via ws.on() (a separate subscription channel).
      "task:message",
      // task:completed / task:failed deliberately NOT here. They go through
      // both the task-prefix invalidate (refreshes the agent-task-snapshot
      // cache) AND the chat-specific ws.on() handlers below. The two
      // channels are independent — onAny dispatch and ws.on are separate
      // subscriptions.
    ]);

    const unsubAny = ws.onAny((msg) => {
      if (specificEvents.has(msg.type)) return;
      if (
        msg.type === "agent:created" ||
        msg.type === "agent:archived" ||
        msg.type === "agent:restored" ||
        msg.type === "agent:deleted"
      ) {
        const wsId = getCurrentWsId();
        if (wsId) {
          qc.invalidateQueries({ queryKey: agentPresenceKeys.workspace(wsId) });
        }
      }
      const prefix = msg.type.split(":")[0] ?? "";
      const refresh = refreshMap[prefix];
      if (refresh) debouncedRefresh(prefix, refresh);
    });

    // --- Specific event handlers (granular cache updates) ---
    // No self-event filtering: actor_id identifies the USER, not the TAB.
    // Filtering by actor_id would block other tabs of the same user.
    // Instead, both mutations and WS handlers use dedup checks to be idempotent.

    const unsubAgentActivity = ws.on("agent:activity", (payload) => {
      applyRunnerActivityRealtime(qc, getCurrentWsId() ?? undefined, payload);
    });

    const unsubAgentPresence = ws.on("agent:presence", (payload) => {
      applyAgentPresenceRealtime(qc, presenceWorkspaceId, payload);
    });

    const unsubIssueUpdated = ws.on("issue:updated", (p) => {
      const { issue } = p as IssueUpdatedPayload;
      if (!issue?.id) return;
      const wsId = getCurrentWsId();
      if (wsId) {
        onIssueUpdated(qc, wsId, issue);
        if (issue.status) {
          onInboxIssueStatusChanged(qc, wsId, issue.id, issue.status);
        }
      }
    });

    const unsubIssueCreated = ws.on("issue:created", (p) => {
      const { issue } = p as IssueCreatedPayload;
      if (!issue) return;
      const wsId = getCurrentWsId();
      if (wsId) onIssueCreated(qc, wsId, issue);
    });

    const unsubIssueDeleted = ws.on("issue:deleted", (p) => {
      const { issue_id } = p as IssueDeletedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) {
        onIssueDeleted(qc, wsId, issue_id);
        onInboxIssueDeleted(qc, wsId, issue_id);
      }
    });

    const unsubIssueLabelsChanged = ws.on("issue_labels:changed", (p) => {
      const { issue_id, labels } = p as IssueLabelsChangedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) onIssueLabelsChanged(qc, wsId, issue_id, labels ?? []);
    });

    const unsubIssueMetadataChanged = ws.on("issue_metadata:changed", (p) => {
      const { issue_id, metadata } = p as IssueMetadataChangedPayload;
      if (!issue_id) return;
      const wsId = getCurrentWsId();
      if (wsId) onIssueMetadataChanged(qc, wsId, issue_id, metadata ?? {});
    });

    const unsubInboxNew = ws.on("inbox:new", async (p) => {
      const { item } = p as InboxNewPayload;
      if (!item) return;
      await handleInboxNew(qc, item);
    });

    // --- Timeline event handlers (global fallback) ---
    // These events are also handled granularly by useIssueTimeline when
    // IssueDetail is mounted. This global handler exists to mark the
    // timeline cache stale for issues whose IssueDetail is *not* mounted,
    // so stale data isn't served on next mount (staleTime: Infinity, set on
    // the QueryClient default, relies on this).
    //
    // `refetchType: "none"` is the load-bearing detail: without it, an
    // active IssueDetail observer would refetch the entire timeline on
    // every comment / activity / reaction event. The refetch replaces
    // every entry's reference and busts React.memo on every CommentCard
    // subtree (visible during AI streaming as a flash across all sibling
    // threads, MUL-1941). Inactive observers don't refetch either way;
    // when IssueDetail mounts later, the stale flag triggers the refetch
    // through `refetchOnMount`. Active observers stay fresh via the
    // granular setQueryData handlers in `useIssueTimeline`.
    const invalidateTimeline = (issueId: string) => {
      qc.invalidateQueries({
        queryKey: issueKeys.timeline(issueId),
        refetchType: "none",
      });
    };

    const unsubCommentCreated = ws.on("comment:created", (p) => {
      const { comment } = p as CommentCreatedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    });

    const unsubCommentUpdated = ws.on("comment:updated", (p) => {
      const { comment } = p as CommentUpdatedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    });

    const unsubCommentDeleted = ws.on("comment:deleted", (p) => {
      const { issue_id } = p as CommentDeletedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    const unsubCommentResolved = ws.on("comment:resolved", (p) => {
      const { comment } = p as CommentResolvedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    });

    const unsubCommentUnresolved = ws.on("comment:unresolved", (p) => {
      const { comment } = p as CommentUnresolvedPayload;
      if (comment?.issue_id) invalidateTimeline(comment.issue_id);
    });

    const unsubActivityCreated = ws.on("activity:created", (p) => {
      const { issue_id } = p as ActivityCreatedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    const unsubReactionAdded = ws.on("reaction:added", (p) => {
      const { issue_id } = p as ReactionAddedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    const unsubReactionRemoved = ws.on("reaction:removed", (p) => {
      const { issue_id } = p as ReactionRemovedPayload;
      if (issue_id) invalidateTimeline(issue_id);
    });

    // --- Issue-level reactions & subscribers (global fallback) ---

    const unsubIssueReactionAdded = ws.on("issue_reaction:added", (p) => {
      const { issue_id } = p as IssueReactionAddedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.reactions(issue_id) });
    });

    const unsubIssueReactionRemoved = ws.on("issue_reaction:removed", (p) => {
      const { issue_id } = p as IssueReactionRemovedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.reactions(issue_id) });
    });

    const unsubSubscriberAdded = ws.on("subscriber:added", (p) => {
      const { issue_id } = p as SubscriberAddedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.subscribers(issue_id) });
    });

    const unsubSubscriberRemoved = ws.on("subscriber:removed", (p) => {
      const { issue_id } = p as SubscriberRemovedPayload;
      if (issue_id) qc.invalidateQueries({ queryKey: issueKeys.subscribers(issue_id) });
    });

    // --- Side-effect handlers (toast, navigation) ---

    // After the current workspace disappears (deleted or we were kicked out),
    // navigate to another workspace the user still has access to, or to the
    // create-workspace page. We use a full-page navigation: this reliably
    // tears down any in-flight queries / subscriptions tied to the dead
    // workspace without relying on framework-specific routers from here in
    // core.
    const relocateAfterWorkspaceLoss = async (lostWsId: string) => {
      const wsList = await qc.fetchQuery({
        ...workspaceListOptions(),
        staleTime: 0,
      });
      const remaining = wsList.filter((w) => w.id !== lostWsId);
      const target = resolvePostAuthDestination(
        remaining,
        hasOnboardedRef.current,
      );
      if (typeof window !== "undefined") {
        window.location.assign(target);
      }
    };

    const unsubWsUpdated = ws.on("workspace:updated", (p) => {
      applyWorkspaceUpdatedToCache(qc, p as WorkspaceUpdatedPayload);
    });

    const unsubWsDeleted = ws.on("workspace:deleted", (p) => {
      const { workspace_id } = p as WorkspaceDeletedPayload;
      // Event payload has UUID; look up slug from cached workspace list
      // since clearWorkspaceStorage keys are namespaced by slug.
      const wsList = qc.getQueryData<{ id: string; slug: string }[]>(workspaceKeys.list()) ?? [];
      const deletedSlug = wsList.find((w) => w.id === workspace_id)?.slug;
      if (deletedSlug) clearWorkspaceStorage(defaultStorage, deletedSlug);
      if (getCurrentWsId() === workspace_id) {
        logger.warn("current workspace deleted, switching");
        onToast?.("This workspace was deleted", "info");
        relocateAfterWorkspaceLoss(workspace_id);
      }
    });

    const unsubMemberRemoved = ws.on("member:removed", (p) => {
      const { user_id } = p as MemberRemovedPayload;
      const myUserId = authStore.getState().user?.id;
      if (user_id === myUserId) {
        const slug = getCurrentSlug();
        const wsId = getCurrentWsId();
        if (slug && wsId) {
          clearWorkspaceStorage(defaultStorage, slug);
          logger.warn("removed from workspace, switching");
          onToast?.("You were removed from this workspace", "info");
          relocateAfterWorkspaceLoss(wsId);
        }
      }
    });

    const unsubMemberAdded = ws.on("member:added", (p) => {
      const { member, workspace_name } = p as MemberAddedPayload;
      const myUserId = authStore.getState().user?.id;
      if (member.user_id === myUserId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.list() });
        qc.invalidateQueries({ queryKey: workspaceKeys.myInvitations() });
        onToast?.(
          `You joined ${workspace_name ?? "a workspace"}`,
          "info",
        );
      }
    });

    // LRM-462: patch online set without refetching the members directory.
    const unsubMemberPresence = ws.on("member:presence", (p) => {
      const payload = p as MemberPresencePayload;
      const wsId = payload.workspace_id || getCurrentWsId();
      if (!wsId || !payload.user_id || !payload.status) return;
      applyMemberPresenceEvent(qc, wsId, {
        user_id: payload.user_id,
        status: payload.status,
        observed_at: payload.observed_at,
      });
    });

    // invitation:created — notify the invitee of a new pending invitation
    const unsubInvitationCreated = ws.on("invitation:created", (p) => {
      const { workspace_name } = p as InvitationCreatedPayload;
      qc.invalidateQueries({ queryKey: workspaceKeys.myInvitations() });
      onToast?.(
        `You were invited to ${workspace_name ?? "a workspace"}`,
        "info",
      );
    });

    // invitation:accepted / declined / revoked — refresh invitation lists
    const unsubInvitationAccepted = ws.on("invitation:accepted", () => {
      const currentWsId = getCurrentWsId();
      if (currentWsId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.invitations(currentWsId) });
        qc.invalidateQueries({ queryKey: workspaceKeys.members(currentWsId) });
      }
    });
    const unsubInvitationDeclined = ws.on("invitation:declined", () => {
      const currentWsId = getCurrentWsId();
      if (currentWsId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.invitations(currentWsId) });
      }
    });
    const unsubInvitationRevoked = ws.on("invitation:revoked", () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.myInvitations() });
    });

    // --- Chat / task events (global, survives ChatWindow unmount) ---
    //
    // Single source of truth: the Query cache. No Zustand writes here — the
    // earlier mirror caused a race where the cache and store disagreed
    // during the invalidate → refetch window and the UI rendered duplicates.
    //
    // task:message is written directly into the task-messages cache so the
    // live timeline updates in place. chat:message / chat:done /
    // task:completed / task:failed invalidate messages + pending-task so the
    // DB remains authoritative.

    const unsubTaskMessage = ws.on("task:message", (p) => {
      const payload = p as TaskMessagePayload;
      qc.setQueryData<TaskMessagePayload[]>(
        ["task-messages", payload.task_id],
        (old = []) => {
          if (old.some((m) => m.seq === payload.seq)) return old;
          return [...old, payload].sort((a, b) => a.seq - b.seq);
        },
      );
      chatWsLogger.debug("task:message (global)", {
        task_id: payload.task_id,
        seq: payload.seq,
        type: payload.type,
      });
    });

    // Helpers reused by chat lifecycle handlers.
    const invalidatePendingAggregate = () => {
      const id = getCurrentWsId();
      if (id) qc.invalidateQueries({ queryKey: chatKeys.pendingTasks(id) });
    };
    const invalidateChatConversationLists = () => {
      const id = getCurrentWsId();
      if (!id) return;
      qc.invalidateQueries({ queryKey: chatKeys.sessions(id) });
      // The Messages view unions legacy chat_sessions into its DM list, so any
      // chat lifecycle change can affect a row preview, unread badge, or order.
      qc.invalidateQueries({ queryKey: dmKeys.list(id) });
      // LRM-1399 — the unified Conversations list is the page's single source
      // for CHANNELS + DMs; keep it fresh on the same chat lifecycle events.
      qc.invalidateQueries({ queryKey: conversationKeys.list(id) });
    };
    const invalidateNoteAIJob = (taskId: string | null | undefined) => {
      if (taskId) qc.invalidateQueries({ queryKey: noteKeys.aiJob(taskId) });
    };

    const unsubChatMessage = ws.on("chat:message", (p) => {
      const payload = p as { chat_session_id: string };
      chatWsLogger.info("chat:message (global)", { chat_session_id: payload.chat_session_id });
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
      // A new message can flip has_unread and reorder the session list — e.g.
      // an agent-initiated DM arriving in a thread the user isn't viewing.
      // Refresh the lists so the contact row / FAB badge update live.
      invalidateChatConversationLists();
    });

    const unsubChatDone = ws.on("chat:done", (p) => {
      const payload = p as ChatDonePayload;
      chatWsLogger.info("chat:done (global)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
        has_message: !!payload.message_id,
      });
      // Inline-insert the assistant message into the messages cache BEFORE
      // clearing pending-task. Both writes land in the same React render
      // tick, so ChatMessageList sees `pendingAlreadyPersisted === true`
      // and the live TimelineView unmounts only after AssistantMessage has
      // mounted — no flicker window. This applies TkDodo's "combine
      // setQueryData (active query) + invalidateQueries (others)" pattern
      // (https://tkdodo.eu/blog/using-web-sockets-with-react-query).
      //
      // Falls back to invalidate-only when the server omits the message
      // payload (older builds). Older clients hitting a newer server also
      // work: they ignore the extra fields and rely on the invalidate
      // below, which keeps the old behavior alive.
      applyChatDoneToCache(qc, payload);
      invalidateNoteAIJob(payload.task_id);
      invalidatePendingAggregate();
      // Assistant message just landed → has_unread may have flipped to true.
      invalidateChatConversationLists();
    });

    // Chat task lifecycle writethrough: keep `chatKeys.pendingTask(sessionId)`
    // synchronized with the server state machine via setQueryData rather than
    // invalidate-refetch. Same pattern as task:message — the WS payload
    // carries everything we need, and an HTTP roundtrip just to read what we
    // already know would add latency to every stage transition.
    //
    // task:queued is emitted by EnqueueChatTask. The optimistic seed in
    // chat-window.tsx may have already populated the cache with a temporary
    // id; this handler upgrades it to the real task_id (and reaffirms status
    // when reconnect replays the event for an already-running task).
    const unsubTaskQueued = ws.on("task:queued", (p) => {
      const payload = p as TaskQueuedPayload;
      applyTaskStatusToSnapshot(payload.task_id, "queued");
      invalidateNoteAIJob(payload.task_id);
      if (!payload.chat_session_id) return;
      invalidatePendingAggregate();
    });

    // task:dispatch fires when the daemon claims the queued task. The daemon
    // immediately follows with StartTask, so dispatched→running is sub-second.
    // We collapse that window by writing "running" directly — the pill jumps
    // from "Queued" straight to "Thinking", skipping a meaningless "Starting"
    // frame. Stage decision in TaskStatusPill maps "running" + empty
    // taskMessages → "Thinking · Ns".
    const unsubTaskDispatch = ws.on("task:dispatch", (p) => {
      const payload = p as TaskDispatchPayload;
      applyTaskStatusToSnapshot(payload.task_id, "dispatched");
      invalidateNoteAIJob(payload.task_id);
      if (!payload.chat_session_id) return;
    });

    // task:running confirms the delivery entered the provider run phase.
    const unsubTaskRunning = ws.on("task:running", (p) => {
      const payload = p as TaskRunningPayload;
      applyTaskStatusToSnapshot(payload.task_id, "running");
      invalidateNoteAIJob(payload.task_id);
      if (!payload.chat_session_id) return;
    });

    // task:cancelled reaches us when:
    //   1. handleStop already cleared the cache locally (this is a no-op confirm)
    //   2. another tab / admin / system cancels — this is the only path that
    //      drops the pending pill in those cases. Without it the pill spins
    //      forever in the second-tab scenario.
    // CancelTask also persists a best-effort assistant snapshot when the
    // stopped chat task had already streamed transcript rows, so refresh the
    // message page along with clearing pending.
    const unsubTaskCancelled = ws.on("task:cancelled", (p) => {
      const payload = p as TaskCancelledPayload;
      applyTaskStatusToSnapshot(payload.task_id, "cancelled");
      invalidateNoteAIJob(payload.task_id);
      if (!payload.chat_session_id) return;
      chatWsLogger.info("task:cancelled (global, chat)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
      });
      qc.setQueryData<ChatPendingTask | Record<string, never>>(
        chatKeys.pendingTask(payload.chat_session_id),
        () => ({}),
      );
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      invalidatePendingAggregate();
    });

    const unsubTaskCompleted = ws.on("task:completed", (p) => {
      const payload = p as TaskCompletedPayload;
      applyTaskStatusToSnapshot(payload.task_id, "completed");
      invalidateNoteAIJob(payload.task_id);
      if (!payload.chat_session_id) return; // issue tasks handled elsewhere
      chatWsLogger.info("task:completed (global, chat)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
      });
      // `chat:done` (broadcast immediately before this event in CompleteTask)
      // already wrote the assistant message into the messages cache and
      // cleared `chatKeys.pendingTask`. This event is now only responsible
      // for refreshing the per-user cross-session aggregate that drives the
      // FAB indicator — `chat:done` is per-session and doesn't carry that
      // information.
      invalidatePendingAggregate();
    });

    const unsubTaskFailed = ws.on("task:failed", (p) => {
      const payload = p as TaskFailedPayload;
      applyTaskStatusToSnapshot(payload.task_id, "failed");
      invalidateNoteAIJob(payload.task_id);
      if (!payload.chat_session_id) return;
      chatWsLogger.warn("task:failed (global, chat)", {
        task_id: payload.task_id,
        chat_session_id: payload.chat_session_id,
      });
      // FailTask writes a failure chat_message (mirroring CompleteTask's
      // success message), so this path mirrors the task:completed handler:
      // clear the pending signal AND invalidate the messages list so the
      // failure bubble shows up without requiring a page refresh. Pre-#1823
      // this branch only flipped pending — the comment "No new message"
      // was true then, but FailTask now persists a row.
      qc.setQueryData(chatKeys.pendingTask(payload.chat_session_id), {});
      invalidateChatMessageQueries(qc, payload.chat_session_id);
      qc.invalidateQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();
    });

    const unsubChatSessionRead = ws.on("chat:session_read", (p) => {
      const payload = p as { chat_session_id: string };
      chatWsLogger.info("chat:session_read (global)", payload);
      invalidateChatConversationLists();
    });

    // chat:session_updated fires after the creator renames a session in
    // any tab/device. Patch the cached row inline so the dropdown reflects
    // the new title without a full sessions-list refetch.
    const unsubChatSessionUpdated = ws.on("chat:session_updated", (p) => {
      const payload = p as {
        chat_session_id: string;
        title?: string;
        updated_at?: string;
      };
      chatWsLogger.info("chat:session_updated (global)", payload);
      const id = getCurrentWsId();
      if (!id) return;
      const patch = (
        old?: { id: string; title: string; updated_at: string }[],
      ) =>
        old?.map((s) =>
          s.id === payload.chat_session_id
            ? {
                ...s,
                title: payload.title ?? s.title,
                updated_at: payload.updated_at ?? s.updated_at,
              }
            : s,
        );
      qc.setQueryData(chatKeys.sessions(id), patch);
    });

    // chat:session_deleted fires after a hard delete. The originating tab has
    // already optimistically dropped the row via useDeleteChatSession; this
    // handler keeps OTHER tabs/devices in sync and also clears the active
    // session pointer so a deleted session doesn't keep the chat window
    // pointed at vanished messages.
    const unsubChatSessionDeleted = ws.on("chat:session_deleted", (p) => {
      const payload = p as { chat_session_id: string };
      chatWsLogger.info("chat:session_deleted (global)", payload);
      const id = getCurrentWsId();
      if (id) {
        const drop = (old?: { id: string }[]) =>
          old?.filter((s) => s.id !== payload.chat_session_id);
        qc.setQueryData(chatKeys.sessions(id), drop);
      }
      qc.removeQueries({ queryKey: chatKeys.messages(payload.chat_session_id) });
      qc.removeQueries({ queryKey: chatKeys.pendingTask(payload.chat_session_id) });
      invalidatePendingAggregate();

      const chatState = useChatStore.getState?.();
      if (chatState && chatState.activeSessionId === payload.chat_session_id) {
        chatState.setActiveSession(null);
      }
    });

    const unsubChannelMessage = ws.on("channel:message", (p) => {
      const payload = p as ChannelMessage;
      if (payload.channel_id && payload.id && !payload.thread_root_message_id) {
        // Show the committed canonical row without waiting for a refetch, then
        // invalidate the channel's two timeline projections. The refetch keeps
        // sparse realtime payloads from becoming the long-lived source of truth.
        upsertChannelMessageInCache(qc, payload);
        invalidateChannelMessages(qc, payload.channel_id);
      } else if (payload.channel_id) {
        invalidateChannelMessages(qc, payload.channel_id);
      }
      if (payload.thread_root_message_id) {
        // Upsert is enough to retire optimistic pending bubbles. An immediate
        // thread refetch can race the ACK (LRM-271/273).
        upsertChannelMessageThreadInCache(qc, payload, payload.thread_root_message_id);
      }
      const id = getCurrentWsId();
      if (id) qc.invalidateQueries({ queryKey: channelKeys.list(id) });
      if (id) qc.invalidateQueries({ queryKey: conversationKeys.list(id) });
      void handleChannelMessageNotification(qc, payload, authStore.getState().user?.id);
    });

    // Server-owned enrichment replaces the existing row without generating a
    // second message notification.
    const unsubChannelMessageUpdated = ws.on("channel:message_updated", (p) => {
      const payload = p as ChannelMessage;
      if (payload.channel_id && payload.id && !payload.thread_root_message_id) {
        upsertChannelMessageInCache(qc, payload);
      } else if (payload.channel_id) {
        invalidateChannelMessages(qc, payload.channel_id);
      }
      if (payload.thread_root_message_id) {
        upsertChannelMessageThreadInCache(qc, payload, payload.thread_root_message_id);
      }
      const id = getCurrentWsId();
      if (id) qc.invalidateQueries({ queryKey: channelKeys.list(id) });
      if (id) qc.invalidateQueries({ queryKey: conversationKeys.list(id) });
    });

    // #689 perf audit: a reaction touches one field on one row. Invalidating
    // the whole channel message list on every reaction re-renders the entire
    // virtualized viewport — the single largest contributor to mobile scroll
    // jank measured. Patch the cached row directly when the event carries
    // enough to compute the new reaction set; fall back to the old
    // full-list invalidate only for a payload shape this can't patch (never
    // silently drop the update).
    const unsubChannelReactionAdded = ws.on("channel_reaction:added", (p) => {
      const payload = p as { channel_id?: string; message_id?: string; reaction?: ChannelReaction };
      if (!payload.channel_id) return;
      // Thread panels aren't the audited jank surface and the payload carries
      // no thread_root_message_id to target one directly — keep this side a
      // plain invalidate (cheap: only the currently-open thread, if any, is
      // subscribed) rather than inventing a broader thread-cache patch.
      qc.invalidateQueries({ queryKey: ["channel-message-thread", payload.channel_id] });
      if (!payload.message_id || !payload.reaction) {
        invalidateChannelMessages(qc, payload.channel_id);
        return;
      }
      const reaction = payload.reaction;
      patchChannelMessageReactionInCache(qc, payload.channel_id, payload.message_id, (reactions) => {
        if (reactions?.some((r) => r.id === reaction.id)) return reactions;
        return [...(reactions ?? []), reaction];
      });
    });

    const unsubChannelReactionRemoved = ws.on("channel_reaction:removed", (p) => {
      const payload = p as {
        channel_id?: string;
        message_id?: string;
        emoji?: string;
        actor_type?: string;
        actor_id?: string;
      };
      if (!payload.channel_id) return;
      qc.invalidateQueries({ queryKey: ["channel-message-thread", payload.channel_id] });
      if (!payload.message_id || !payload.emoji || !payload.actor_type || !payload.actor_id) {
        invalidateChannelMessages(qc, payload.channel_id);
        return;
      }
      const { emoji, actor_type, actor_id } = payload;
      patchChannelMessageReactionInCache(qc, payload.channel_id, payload.message_id, (reactions) =>
        reactions?.filter(
          (r) => !(r.emoji === emoji && r.actor_type === actor_type && r.actor_id === actor_id),
        ),
      );
    });

    const unsubChannelUpdated = ws.on("channel:updated", (rawPayload) => {
      const payload = rawPayload && typeof rawPayload === "object"
        ? rawPayload as { id?: unknown; work_graph_id?: unknown }
        : {};
      const channelId = typeof payload.id === "string" ? payload.id : "";
      const graphId = typeof payload.work_graph_id === "string" ? payload.work_graph_id : "";
      if (channelId && graphId) {
        debouncedRefresh(`work-graph:${graphId}`, () => {
          qc.invalidateQueries({ queryKey: channelGoalKeys.detail(channelId), exact: true });
          qc.invalidateQueries({ queryKey: channelGoalKeys.workGraph(graphId), exact: true });
        });
        return;
      }
      const id = getCurrentWsId();
      // Include archived-list (archive/restore) — list alone leaves Archived stale.
      if (id) qc.invalidateQueries({ queryKey: channelKeys.all(id) });
      if (id) qc.invalidateQueries({ queryKey: conversationKeys.list(id) });
      if (channelId) {
        qc.invalidateQueries({ queryKey: channelGoalKeys.detail(channelId), exact: true });
      } else {
        qc.invalidateQueries({ queryKey: channelGoalKeys.all() });
      }
    });

    const unsubVoiceCallUpdated = ws.on("voice_call:updated", (payload) => {
      invalidateVoiceCallFromRealtime(qc, payload as VoiceCallUpdatedPayload);
    });

    const researchWsId = () => getCurrentWsId() ?? "";
    const onResearchEvent = (type: WSMessage["type"]) => (payload: unknown) => {
      const wsId = researchWsId();
      if (!wsId) return;
      applyResearchWSEvent(qc, wsId, { type, payload } as WSMessage);
    };
    const unsubResearchGraph = ws.on("research_session:graph_updated", onResearchEvent("research_session:graph_updated"));
    const unsubResearchPresence = ws.on("research_session:presence", onResearchEvent("research_session:presence"));
    const unsubResearchSources = ws.on("research_session:sources_updated", onResearchEvent("research_session:sources_updated"));
    const unsubResearchReport = ws.on("research_session:report_updated", onResearchEvent("research_session:report_updated"));
    const unsubResearchMessage = ws.on("research_session:message", onResearchEvent("research_session:message"));
    const unsubResearchStageEval = ws.on("research_session:stage_eval", onResearchEvent("research_session:stage_eval"));
    const unsubResearchStatus = ws.on("research_session:status_changed", onResearchEvent("research_session:status_changed"));
    const unsubResearchProductRound = ws.on("research_session:product_round", onResearchEvent("research_session:product_round"));

    return () => {
      unsubAny();
      unsubAgentActivity();
      unsubAgentPresence();
      unsubIssueUpdated();
      unsubIssueCreated();
      unsubIssueDeleted();
      unsubIssueLabelsChanged();
      unsubIssueMetadataChanged();
      unsubInboxNew();
      unsubCommentCreated();
      unsubCommentUpdated();
      unsubCommentDeleted();
      unsubCommentResolved();
      unsubCommentUnresolved();
      unsubActivityCreated();
      unsubReactionAdded();
      unsubReactionRemoved();
      unsubIssueReactionAdded();
      unsubIssueReactionRemoved();
      unsubSubscriberAdded();
      unsubSubscriberRemoved();
      unsubWsUpdated();
      unsubWsDeleted();
      unsubMemberRemoved();
      unsubMemberAdded();
      unsubMemberPresence();
      unsubInvitationCreated();
      unsubInvitationAccepted();
      unsubInvitationDeclined();
      unsubInvitationRevoked();
      unsubTaskMessage();
      unsubChatMessage();
      unsubChatDone();
      unsubTaskQueued();
      unsubTaskDispatch();
      unsubTaskRunning();
      unsubTaskCancelled();
      unsubTaskCompleted();
      unsubTaskFailed();
      unsubChatSessionRead();
      unsubChatSessionDeleted();
      unsubChatSessionUpdated();
      unsubChannelMessage();
      unsubChannelMessageUpdated();
      unsubChannelReactionAdded();
      unsubChannelReactionRemoved();
      unsubChannelUpdated();
      unsubVoiceCallUpdated();
      unsubResearchGraph();
      unsubResearchPresence();
      unsubResearchSources();
      unsubResearchReport();
      unsubResearchMessage();
      unsubResearchStageEval();
      unsubResearchStatus();
      unsubResearchProductRound();
      timers.forEach(clearTimeout);
      timers.clear();
    };
  }, [ws, qc, authStore, onToast]);

  // Reconnect -> refetch all data to recover missed events
  useEffect(() => {
    if (!ws) return;

    const unsub = ws.onReconnect(async () => {
      logger.info("reconnected, refetching all data");
      try {
        invalidateWorkspaceScopedQueries(qc);
      } catch (e) {
        logger.error("reconnect refetch failed", e);
      }
    });

    return unsub;
  }, [ws, qc]);

  // New WSClient instance (workspace switch) -> invalidate workspace-scoped
  // queries to recover events missed while the previous instance was torn down.
  // Skips the initial assignment to avoid a redundant refetch on first mount.
  const wsInstanceRef = useRef<WSClient | null>(null);
  useEffect(() => {
    if (!ws) return;
    if (wsInstanceRef.current === null) {
      // First non-null instance — store and skip invalidation.
      wsInstanceRef.current = ws;
      return;
    }
    if (wsInstanceRef.current === ws) return;
    wsInstanceRef.current = ws;

    logger.info("new WSClient instance detected, invalidating workspace queries");
    invalidateWorkspaceScopedQueries(qc);
  }, [ws, qc]);
}
