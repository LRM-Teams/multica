"use client";

import { useEffect, useCallback, useRef, lazy, Suspense } from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useModalStore } from "@multica/core/modals";
import { useIssueDraftStore } from "@multica/core/issues/stores/draft-store";
import {
  userActivityListOptions,
  useUserActivityUnreadCount,
} from "@multica/core/user-activity/queries";
import { useMarkAllUserActivityRead } from "@multica/core/user-activity/mutations";
import {
  useMarkInboxRead,
  useArchiveInbox,
} from "@multica/core/inbox/mutations";
import { useMarkChannelThreadRead } from "@multica/core/channels/mutations";
import type { InboxItem, UserActivityItem, UserActivityTab } from "@multica/core/types";

import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { useNavigation } from "../../navigation";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Activity, Archive, ArrowLeft, ExternalLink } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  ResizablePanelGroup,
  ResizablePanel,
  ResizableHandle,
} from "@multica/ui/components/ui/resizable";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { PageHeader } from "../../layout/page-header";
import { useT, Time } from "../../i18n";
import { useTypeLabels } from "./inbox-detail-label";
import { getInboxDisplayTitle } from "./inbox-display";
import { ActivityListRow } from "./activity-list-row";
import { ActivityTabs, ActivityEmptyState } from "./activity-tabs";
import { ActivityListSkeleton } from "./activity-list-skeleton";
import {
  activityItemMatchesSelection,
  activitySessionParams,
  activitySessionUrl,
  resolveActivitySessionSurface,
} from "./activity-session";
import { MobileListDetailLayout } from "../../common/mobile-list-detail-layout";

/** Static Suspense fallback — hoist so React Doctor does not rebuild each render (LRM-424). */
const ACTIVITY_DETAIL_FALLBACK = (
  <div className="space-y-3 p-6" data-testid="activity-detail-fallback">
    <Skeleton className="h-6 w-48" />
    <Skeleton className="h-4 w-32" />
    <Skeleton className="h-24 w-full" />
  </div>
);

// LRM-424: IssueDetail / ChannelsPage are heavy. Lazy-load so the list shell
// paints without waiting on those graphs (also avoids regressing LRM-400 when
// a session is already selected on re-entry).
const IssueDetail = lazy(() =>
  import("../../issues/components/issue-detail").then((m) => ({
    default: m.IssueDetail,
  })),
);

const ChannelsPage = lazy(() =>
  import("../../channels/components/channels-page").then((m) => ({
    default: m.ChannelsPage,
  })),
);

function inboxItemFromActivity(item: UserActivityItem): InboxItem | null {
  return item.kind === "inbox" ? item.inbox ?? null : null;
}

function parseActivityTab(raw: string | null): UserActivityTab {
  if (raw === "unread" || raw === "mentions") return raw;
  return "all";
}

export function InboxPage() {
  const { t } = useT("inbox");
  const { searchParams, replace, push } = useNavigation();
  const urlIssue = searchParams.get("issue") ?? "";
  const urlChannel = searchParams.get("channel") ?? "";
  const urlThread = searchParams.get("thread") ?? "";
  const urlMessage = searchParams.get("message") ?? "";
  const tab = parseActivityTab(searchParams.get("tab"));
  const wsPaths = useWorkspacePaths();
  const inboxPath = wsPaths.inbox();

  const wsId = useWorkspaceId();
  const {
    data: activityData,
    isLoading,
    isError,
    error,
    refetch,
    isFetching,
  } = useQuery(userActivityListOptions(wsId, tab));
  const items = activityData?.items ?? [];
  // isLoading = pending && fetching with no data yet. Cached / placeholder rows
  // keep the list painted while refetchOnMount silently refreshes (LRM-424).
  const showListSkeleton = isLoading;

  const selectedKey = urlIssue || urlThread || urlMessage;
  // Sticky session: Unread tab mark-read optimistically drops the row from the
  // feed (LRM-379). Keep the last selected item so the right pane / mobile
  // push still opens thread|channel instead of clearing back to empty.
  const stickySelectedRef = useRef<UserActivityItem | null>(null);
  const selectedFromList = selectedKey
    ? (items.find((item) => activityItemMatchesSelection(item, selectedKey)) ??
      null)
    : null;
  const stickySelected = stickySelectedRef.current;
  const stickyMatches =
    !!selectedKey &&
    !!stickySelected &&
    activityItemMatchesSelection(stickySelected, selectedKey);
  const selectedItem = selectedFromList ?? (stickyMatches ? stickySelected : null);
  const selectedInbox = selectedItem ? inboxItemFromActivity(selectedItem) : null;
  const selectedThread =
    selectedItem?.kind === "thread" ? selectedItem : null;
  const sessionSurface = selectedThread
    ? resolveActivitySessionSurface(selectedThread)
    : null;

  const lastResolvedKeyRef = useRef<string>("");
  useEffect(() => {
    if (selectedFromList) stickySelectedRef.current = selectedFromList;
  }, [selectedFromList]);
  useEffect(() => {
    if (selectedInbox || selectedThread) lastResolvedKeyRef.current = selectedKey;
  }, [selectedInbox, selectedThread, selectedKey]);

  const replaceActivityUrl = useCallback(
    (params: Parameters<typeof activitySessionUrl>[1]) => {
      replace(activitySessionUrl(inboxPath, { ...params, tab }));
    },
    [replace, inboxPath, tab],
  );

  const clearSelection = useCallback(() => {
    stickySelectedRef.current = null;
    replaceActivityUrl(tab !== "all" ? { tab } : {});
  }, [replaceActivityUrl, tab]);

  const setTab = useCallback(
    (next: UserActivityTab) => {
      replace(
        activitySessionUrl(inboxPath, {
          tab: next,
          issue: urlIssue || undefined,
          channel: urlChannel || undefined,
          thread: urlThread || undefined,
          message: urlMessage || undefined,
        }),
      );
    },
    [replace, inboxPath, urlIssue, urlChannel, urlThread, urlMessage],
  );

  useEffect(() => {
    // Wait out cold load + in-flight refetch (incl. keepPreviousData placeholders)
    // before treating a deep-link as missing from the feed.
    if (isLoading || isFetching) return;
    if (!selectedKey) return;
    if (selectedInbox || selectedThread) return;
    // Channel/thread/message deep-link: keep the URL even when Unread mark-read
    // dropped the row (sticky or ChannelsPage resolves the session from params).
    if (urlChannel && (urlThread || urlMessage)) return;
    if (lastResolvedKeyRef.current === selectedKey) {
      clearSelection();
      return;
    }
    // Legacy deep-link: bare issue id that is not in the Activity feed →
    // open the issue route. Do not silently invent a session pane (LRM-238).
    if (urlIssue && !urlChannel) {
      replace(wsPaths.issueDetail(urlIssue));
      return;
    }
    clearSelection();
  }, [
    isLoading,
    isFetching,
    selectedKey,
    selectedInbox,
    selectedThread,
    clearSelection,
    replace,
    wsPaths,
    urlIssue,
    urlChannel,
    urlThread,
    urlMessage,
  ]);

  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_inbox_layout",
  });

  const isMobile = useIsMobile();
  const unreadCount = useUserActivityUnreadCount(wsId);

  const markReadMutation = useMarkInboxRead();
  const archiveMutation = useArchiveInbox();
  const markAllReadMutation = useMarkAllUserActivityRead();
  const markThreadReadMutation = useMarkChannelThreadRead();
  const typeLabels = useTypeLabels();

  const markReadMutate = markReadMutation.mutate;
  const markThreadReadMutate = markThreadReadMutation.mutate;
  const selectedInboxId = selectedInbox?.id;
  const selectedInboxRead = selectedInbox?.read;
  useEffect(() => {
    if (!selectedInboxId || selectedInboxRead) return;
    markReadMutate(selectedInboxId, {
      onError: (err) =>
        showErrorToast(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.errors.mark_read_failed),
        ),
    });
  }, [selectedInboxId, selectedInboxRead, markReadMutate, t]);

  const handleSelect = (item: UserActivityItem) => {
    if (item.access_denied) return;

    if (item.kind === "thread") {
      const channelId = item.channel_id;
      const rootId = item.thread_root_message_id ?? item.id;
      if (!channelId) {
        showErrorToast(t(($) => $.activity.open_thread_failed));
        return;
      }
      // Pin before URL/mark-read so Unread optimistic drop cannot blank the pane.
      stickySelectedRef.current = item;
      // Mark read on click (LRM-379): do not rely solely on ThreadPanel open —
      // deep-link may miss the root in the loaded message window, and Activity
      // cache was previously never invalidated after thread/read.
      if (item.unread_count > 0) {
        markThreadReadMutate(
          { channelId, messageId: rootId },
          {
            onError: (err) =>
              showErrorToast(
                err instanceof Error && err.message
                  ? err.message
                  : t(($) => $.errors.mark_read_failed),
              ),
          },
        );
      }
      replaceActivityUrl(activitySessionParams(item, tab));
      return;
    }

    const inbox = inboxItemFromActivity(item);
    if (!inbox) {
      showErrorToast(t(($) => $.activity.open_item_failed));
      return;
    }
    stickySelectedRef.current = item;
    replaceActivityUrl(activitySessionParams(item, tab));
  };

  const handleArchive = (id: string) => {
    if (selectedInbox && selectedInbox.id === id) {
      clearSelection();
    }
    archiveMutation.mutate(id, {
      onError: (err) =>
        showErrorToast(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.errors.archive_failed),
        ),
    });
  };

  const handleMarkAllRead = () => {
    markAllReadMutation.mutate(undefined, {
      onError: (err) =>
        showErrorToast(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.errors.mark_all_read_failed),
        ),
    });
  };

  const openInChannels = useCallback(
    (opts: { channelId: string; messageId?: string; threadId?: string }) => {
      const search = new URLSearchParams();
      if (opts.threadId) search.set("thread", opts.threadId);
      if (opts.messageId) search.set("message", opts.messageId);
      const qs = search.toString();
      push(
        qs
          ? `${wsPaths.channelDetail(opts.channelId)}?${qs}`
          : wsPaths.channelDetail(opts.channelId),
      );
    },
    [push, wsPaths],
  );

  const listHeader = (
    <>
      <PageHeader className="justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <h1 className="text-sm font-semibold">{t(($) => $.page.title)}</h1>
          {unreadCount > 0 ? (
            <span className="text-xs text-muted-foreground">{unreadCount}</span>
          ) : null}
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="h-8 shrink-0 px-2 text-brand hover:text-brand"
          onClick={handleMarkAllRead}
          disabled={markAllReadMutation.isPending || unreadCount === 0}
        >
          {t(($) => $.menu.mark_all_read)}
        </Button>
      </PageHeader>
      <ActivityTabs value={tab} onChange={setTab} unreadCount={unreadCount} />
    </>
  );

  const listBody = isError && !activityData ? (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <p className="text-sm text-destructive">
        {error instanceof Error && error.message
          ? error.message
          : t(($) => $.activity.load_failed)}
      </p>
      <Button variant="outline" size="sm" onClick={() => refetch()}>
        {t(($) => $.activity.retry)}
      </Button>
    </div>
  ) : showListSkeleton ? (
    <ActivityListSkeleton />
  ) : items.length === 0 ? (
    <ActivityEmptyState tab={tab} />
  ) : (
    <div>
      {items.map((item) => (
        <ActivityListRow
          key={`${item.kind}-${item.id}`}
          item={item}
          isSelected={
            !!selectedKey && activityItemMatchesSelection(item, selectedKey)
          }
          onClick={() => handleSelect(item)}
        />
      ))}
    </div>
  );

  const sessionChannelId =
    selectedThread?.channel_id ?? (urlChannel || null);
  // Prefer feed/sticky surface; if the Unread row is gone, derive from URL
  // (`message` → channel stream, `thread` → let ChannelsPage read ?thread=).
  const urlOnlySurface =
    !sessionSurface && urlChannel
      ? urlMessage
        ? ("channel" as const)
        : urlThread
          ? ("thread" as const)
          : null
      : null;
  const effectiveSurface = sessionSurface ?? urlOnlySurface;
  const hasChannelSession = !!(
    sessionChannelId &&
    (selectedThread || (urlChannel && (urlThread || urlMessage)))
  );

  const sessionDetail = hasChannelSession && sessionChannelId ? (
      <ErrorBoundary
        resetKeys={[sessionChannelId, urlThread, urlMessage, effectiveSurface ?? ""]}
      >
        <div className="flex h-full min-h-0 flex-col" data-testid="activity-session-pane">
          {effectiveSurface === "channel" ? (
            <div className="flex h-10 shrink-0 items-center justify-between gap-2 border-b px-3">
              <span className="truncate text-sm font-semibold">
                {selectedThread?.channel_name
                  ? `#${selectedThread.channel_name}`
                  : t(($) => $.page.title)}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-8 shrink-0 gap-1.5 px-2 text-xs text-brand hover:text-brand"
                onClick={() =>
                  openInChannels({
                    channelId: sessionChannelId,
                    messageId: urlMessage || selectedThread?.id,
                  })
                }
              >
                <ExternalLink className="size-3.5" aria-hidden />
                {t(($) => $.activity.open_in_channels)}
              </Button>
            </div>
          ) : null}
          <div className="flex min-h-0 flex-1 flex-col">
            <Suspense fallback={ACTIVITY_DETAIL_FALLBACK}>
              <ChannelsPage
                channelId={sessionChannelId}
                embedded
                embeddedSurface={
                  effectiveSurface === "dm" || !effectiveSurface
                    ? undefined
                    : effectiveSurface
                }
                onOpenInChannels={openInChannels}
              />
            </Suspense>
          </div>
        </div>
      </ErrorBoundary>
    ) : null;

  const detailContent = sessionDetail ? (
    sessionDetail
  ) : selectedInbox?.issue_id ? (
    <ErrorBoundary resetKeys={[selectedInbox.issue_id]}>
      <Suspense fallback={ACTIVITY_DETAIL_FALLBACK}>
        <IssueDetail
          key={selectedInbox.issue_id}
          issueId={selectedInbox.issue_id}
          defaultSidebarOpen={false}
          layoutId="multica_inbox_issue_detail_layout"
          highlightCommentId={selectedInbox.details?.comment_id ?? undefined}
          onDelete={() => {
            clearSelection();
          }}
          onDone={() => {
            if (selectedInbox) handleArchive(selectedInbox.id);
          }}
        />
      </Suspense>
    </ErrorBoundary>
  ) : selectedInbox ? (
    <div className="p-6">
      <h2 className="text-lg font-semibold">{getInboxDisplayTitle(selectedInbox)}</h2>
      <p className="mt-1 text-sm text-muted-foreground">
        {typeLabels[selectedInbox.type]} · <Time kind="relative" value={selectedInbox.created_at} />
      </p>
      {selectedInbox.body ? (
        <div className="mt-4 whitespace-pre-wrap text-sm leading-relaxed text-foreground/80">
          {selectedInbox.body}
        </div>
      ) : null}
      {selectedInbox.type === "quick_create_failed" &&
      selectedInbox.details?.original_prompt ? (
        <div className="mt-4 rounded-md border bg-muted/40 p-3">
          <p className="text-xs font-medium text-muted-foreground">
            {t(($) => $.detail.original_input)}
          </p>
          <p className="mt-1 whitespace-pre-wrap text-sm">
            {selectedInbox.details.original_prompt}
          </p>
        </div>
      ) : null}
      <div className="mt-4 flex flex-wrap gap-2">
        {selectedInbox.type === "quick_create_failed" ? (
          <Button
            size="sm"
            onClick={() => {
              const prompt = selectedInbox.details?.original_prompt ?? "";
              const agentId = selectedInbox.details?.agent_id;
              useIssueDraftStore.getState().setDraft({
                description: prompt,
                ...(agentId
                  ? { assigneeType: "agent" as const, assigneeId: agentId }
                  : {}),
              });
              useModalStore.getState().open("create-issue");
            }}
          >
            {t(($) => $.detail.edit_advanced)}
          </Button>
        ) : null}
        <Button
          variant="outline"
          size="sm"
          onClick={() => handleArchive(selectedInbox.id)}
        >
          <Archive className="mr-1.5 h-3.5 w-3.5" />
          {t(($) => $.detail.archive)}
        </Button>
      </div>
    </div>
  ) : null;

  const hasDetail = !!(selectedInbox || hasChannelSession);

  if (isMobile) {
    return (
      <MobileListDetailLayout
        showDetail={hasDetail}
        list={
          <>
            {listHeader}
            <div className="min-h-0 flex-1 overflow-y-auto">{listBody}</div>
          </>
        }
        detail={
          <>
            <div className="flex h-12 shrink-0 items-center border-b px-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={clearSelection}
                className="gap-1.5 text-muted-foreground"
              >
                <ArrowLeft className="h-4 w-4" />
                {t(($) => $.page.back)}
              </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-hidden">{detailContent}</div>
          </>
        }
      />
    );
  }

  return (
    <ResizablePanelGroup
      orientation="horizontal"
      className="min-h-0 flex-1"
      defaultLayout={defaultLayout}
      onLayoutChanged={onLayoutChanged}
    >
      <ResizablePanel
        id="list"
        defaultSize={320}
        minSize={240}
        maxSize={480}
        groupResizeBehavior="preserve-pixel-size"
      >
        <div className="flex h-full flex-col border-r">
          {listHeader}
          <div className="min-h-0 flex-1 overflow-y-auto">{listBody}</div>
        </div>
      </ResizablePanel>
      <ResizableHandle />
      <ResizablePanel id="detail" minSize="40%">
        <div className="flex h-full min-h-0 flex-col">
          {detailContent ?? (
            <div className="flex h-full flex-col items-center justify-center text-muted-foreground">
              <Activity className="mb-3 h-10 w-10 text-muted-foreground/30" />
              <p className="text-sm">
                {showListSkeleton
                  ? t(($) => $.detail.select_prompt)
                  : items.length === 0
                    ? t(($) => $.activity.empty.all.title)
                    : t(($) => $.detail.select_prompt)}
              </p>
            </div>
          )}
        </div>
      </ResizablePanel>
    </ResizablePanelGroup>
  );
}
