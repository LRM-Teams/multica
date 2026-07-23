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
import { toast } from "sonner";
import { Activity, Archive, ArrowLeft } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  ResizablePanelGroup,
  ResizablePanel,
  ResizableHandle,
} from "@multica/ui/components/ui/resizable";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { PageHeader } from "../../layout/page-header";
import { useTimeAgo } from "./inbox-list-item";
import { useTypeLabels } from "./inbox-detail-label";
import { getInboxDisplayTitle } from "./inbox-display";
import { ActivityListRow } from "./activity-list-row";
import { ActivityTabs, ActivityEmptyState } from "./activity-tabs";
import { ActivityListSkeleton } from "./activity-list-skeleton";
import { useT } from "../../i18n";
import { MobileListDetailLayout } from "../../common/mobile-list-detail-layout";

// LRM-424: IssueDetail pulls ~50+ modules into the Activity route. Lazy-load so
// list shell paints without waiting on the issue detail graph. Session embed
// (ChannelsPage, LRM-388) should stay lazy the same way when that lands.
const IssueDetail = lazy(() =>
  import("../../issues/components/issue-detail").then((m) => ({
    default: m.IssueDetail,
  })),
);

function inboxItemFromActivity(item: UserActivityItem): InboxItem | null {
  return item.kind === "inbox" ? item.inbox ?? null : null;
}

function activitySelectionKey(item: UserActivityItem): string {
  if (item.kind === "inbox") {
    const inbox = item.inbox;
    return inbox?.issue_id ?? inbox?.id ?? item.id;
  }
  return item.id;
}

function parseActivityTab(raw: string | null): UserActivityTab {
  if (raw === "unread" || raw === "mentions") return raw;
  return "all";
}

function inboxActivityUrl(
  inboxPath: string,
  params: { tab?: UserActivityTab; issue?: string },
): string {
  const search = new URLSearchParams();
  if (params.tab && params.tab !== "all") search.set("tab", params.tab);
  if (params.issue) search.set("issue", params.issue);
  const qs = search.toString();
  return qs ? `${inboxPath}?${qs}` : inboxPath;
}

export function InboxPage() {
  const { t } = useT("inbox");
  const { searchParams, replace, push } = useNavigation();
  const urlIssue = searchParams.get("issue") ?? "";
  const tab = parseActivityTab(searchParams.get("tab"));
  const selectedKey = urlIssue;
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

  const selectedItem =
    items.find((item) => activitySelectionKey(item) === selectedKey) ?? null;
  const selectedInbox = selectedItem ? inboxItemFromActivity(selectedItem) : null;

  const lastResolvedKeyRef = useRef<string>("");
  useEffect(() => {
    if (selectedInbox) lastResolvedKeyRef.current = selectedKey;
  }, [selectedInbox, selectedKey]);

  const setSelectedKey = useCallback(
    (key: string) => {
      replace(inboxActivityUrl(inboxPath, { tab, issue: key || undefined }));
    },
    [replace, inboxPath, tab],
  );

  const setTab = useCallback(
    (next: UserActivityTab) => {
      replace(inboxActivityUrl(inboxPath, { tab: next, issue: urlIssue || undefined }));
    },
    [replace, inboxPath, urlIssue],
  );

  useEffect(() => {
    // Wait out cold load + in-flight refetch (incl. keepPreviousData placeholders)
    // before treating a deep-link as missing from the feed.
    if (isLoading || isFetching) return;
    if (!selectedKey) return;
    if (selectedInbox) return;
    if (lastResolvedKeyRef.current === selectedKey) {
      setSelectedKey("");
      return;
    }
    replace(wsPaths.issueDetail(selectedKey));
  }, [
    isLoading,
    isFetching,
    selectedKey,
    selectedInbox,
    replace,
    wsPaths,
    setSelectedKey,
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
  const timeAgo = useTimeAgo();
  const typeLabels = useTypeLabels();

  const markReadMutate = markReadMutation.mutate;
  const markThreadReadMutate = markThreadReadMutation.mutate;
  const selectedInboxId = selectedInbox?.id;
  const selectedInboxRead = selectedInbox?.read;
  useEffect(() => {
    if (!selectedInboxId || selectedInboxRead) return;
    markReadMutate(selectedInboxId, {
      onError: (err) =>
        toast.error(
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
        toast.error(t(($) => $.activity.open_thread_failed));
        return;
      }
      // Mark read on click (LRM-379): do not rely solely on ThreadPanel open —
      // deep-link may miss the root in the loaded message window, and Activity
      // cache was previously never invalidated after thread/read.
      if (item.unread_count > 0) {
        markThreadReadMutate(
          { channelId, messageId: rootId },
          {
            onError: (err) =>
              toast.error(
                err instanceof Error && err.message
                  ? err.message
                  : t(($) => $.errors.mark_read_failed),
              ),
          },
        );
      }
      push(`${wsPaths.channelDetail(channelId)}?thread=${encodeURIComponent(rootId)}`);
      return;
    }

    const inbox = inboxItemFromActivity(item);
    if (!inbox) {
      toast.error(t(($) => $.activity.open_item_failed));
      return;
    }
    setSelectedKey(inbox.issue_id ?? inbox.id);
  };

  const handleArchive = (id: string) => {
    if (selectedInbox && selectedInbox.id === id) {
      setSelectedKey("");
    }
    archiveMutation.mutate(id, {
      onError: (err) =>
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.errors.archive_failed),
        ),
    });
  };

  const handleMarkAllRead = () => {
    markAllReadMutation.mutate(undefined, {
      onError: (err) =>
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.errors.mark_all_read_failed),
        ),
    });
  };

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
          isSelected={activitySelectionKey(item) === selectedKey}
          onClick={() => handleSelect(item)}
          timeAgo={timeAgo}
        />
      ))}
    </div>
  );

  const detailFallback = (
    <div className="space-y-3 p-6" data-testid="activity-detail-fallback">
      <Skeleton className="h-6 w-48" />
      <Skeleton className="h-4 w-32" />
      <Skeleton className="h-24 w-full" />
    </div>
  );

  const detailContent = selectedInbox?.issue_id ? (
    <ErrorBoundary resetKeys={[selectedInbox.issue_id]}>
      <Suspense fallback={detailFallback}>
        <IssueDetail
          key={selectedInbox.issue_id}
          issueId={selectedInbox.issue_id}
          defaultSidebarOpen={false}
          layoutId="multica_inbox_issue_detail_layout"
          highlightCommentId={selectedInbox.details?.comment_id ?? undefined}
          onDelete={() => {
            setSelectedKey("");
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
        {typeLabels[selectedInbox.type]} · {timeAgo(selectedInbox.created_at)}
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
      <div className="mt-4 flex gap-2">
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

  if (isMobile) {
    return (
      <MobileListDetailLayout
        showDetail={!!selectedInbox}
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
                onClick={() => setSelectedKey("")}
                className="gap-1.5 text-muted-foreground"
              >
                <ArrowLeft className="h-4 w-4" />
                {t(($) => $.page.back)}
              </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">{detailContent}</div>
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
