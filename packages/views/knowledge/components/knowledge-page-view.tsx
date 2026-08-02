"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, X } from "lucide-react";
import type { KnowledgeEdge, TeamKnowledgeListItem } from "@multica/core/types";
import { ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { knowledgeItemOptions, knowledgeNeighborsOptions } from "@multica/core/knowledge";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { Markdown } from "../../common/markdown";
import { useTimeAgo, useT } from "../../i18n";
import { AppLink, useNavigation } from "../../navigation";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { WikiEdgeList } from "./wiki-edge-list";

const LOADING_LEAF = <span className="text-muted-foreground">…</span>;

export function KnowledgePageView({ pageId }: { pageId: string }) {
  const { t } = useT("knowledge");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { push } = useNavigation();

  const {
    data: page,
    error: pageError,
    isLoading: pageLoading,
    isSuccess: pageReady,
  } = useQuery(knowledgeItemOptions(wsId ?? "", pageId));
  const { data: neighbors, isLoading: neighborsLoading } = useQuery({
    ...knowledgeNeighborsOptions(wsId ?? "", pageId, 1),
    enabled: !!wsId && !!pageId && pageReady,
  });

  const status = pageError instanceof ApiError ? pageError.status : 0;

  if (pageLoading) {
    return (
      <div className="flex h-full min-h-0 flex-col" data-testid="knowledge-page-loading">
        <BreadcrumbHeader
          segments={[{ href: paths.wiki(), label: t(($) => $.page.title) }]}
          leaf={LOADING_LEAF}
        />
        <div className="space-y-3 px-4 py-4 sm:px-6">
          <Skeleton className="h-5 w-28" />
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-4 w-40" />
          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_280px]">
            <Skeleton className="h-56 w-full" />
            <Skeleton className="h-56 w-full" />
          </div>
        </div>
      </div>
    );
  }

  if (status === 403) {
    return (
      <StateShell
        title={t(($) => $.page.forbidden)}
        onBack={() => push(paths.wiki())}
        backLabel={t(($) => $.page.back_to_list)}
        testId="knowledge-page-forbidden"
      />
    );
  }

  if (status === 404 || !page?.id) {
    return (
      <StateShell
        title={t(($) => $.page.not_found_title)}
        body={t(($) => $.page.not_found_body)}
        onBack={() => push(paths.wiki())}
        backLabel={t(($) => $.page.back_to_list)}
        testId="knowledge-page-not-found"
      />
    );
  }

  return (
    <KnowledgePageBody
      page={page}
      neighborEdges={neighbors?.edges ?? []}
      neighborsLoading={neighborsLoading}
    />
  );
}

function KnowledgePageBody({
  page,
  neighborEdges,
  neighborsLoading,
}: {
  page: TeamKnowledgeListItem;
  neighborEdges: KnowledgeEdge[];
  neighborsLoading: boolean;
}) {
  const { t } = useT("knowledge");
  const paths = useWorkspacePaths();
  const { push } = useNavigation();
  const timeAgo = useTimeAgo();
  const backLabel = t(($) => $.page.back_to_list);
  const wikiHref = paths.wiki();

  const kindLabel =
    page.kind === "context"
      ? t(($) => $.page.kind.context)
      : page.kind === "decision"
        ? t(($) => $.page.kind.decision)
        : page.kind === "goal"
          ? t(($) => $.page.kind.goal)
          : t(($) => $.page.kind.other);

  const sectionLabel =
    page.kind === "context"
      ? t(($) => $.page.tabs.topic)
      : page.kind === "decision"
        ? t(($) => $.page.tabs.decision)
        : page.kind === "goal"
          ? t(($) => $.page.tabs.goal)
          : t(($) => $.page.kind.other);

  const titleLeaf = useMemo(
    () => <span className="truncate font-medium">{page.title}</span>,
    [page.title],
  );
  const closeAction = useMemo(
    () => (
      <Button
        type="button"
        size="icon"
        variant="ghost"
        className="size-8 md:hidden"
        aria-label={backLabel}
        onClick={() => push(wikiHref)}
      >
        <X className="size-4" />
      </Button>
    ),
    [backLabel, push, wikiHref],
  );

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="knowledge-page-view">
      <BreadcrumbHeader
        segments={[
          { href: wikiHref, label: t(($) => $.page.title) },
          {
            href: `${wikiHref}?tab=${page.kind === "context" ? "topic" : page.kind === "decision" ? "decision" : "all"}`,
            label: sectionLabel,
          },
        ]}
        leaf={titleLeaf}
        actions={closeAction}
      />

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6 pb-[max(1rem,env(safe-area-inset-bottom))]">
        <div className="mb-1 md:hidden">
          <AppLink
            href={wikiHref}
            className={cn(buttonVariants({ variant: "ghost", size: "sm" }), "-ml-2 gap-1")}
          >
            <ArrowLeft className="size-3.5" />
            {backLabel}
          </AppLink>
        </div>

        <div className="mb-4">
          <span className="inline-flex rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
            {kindLabel}
          </span>
          <h1 className="mt-2 text-xl font-semibold tracking-tight sm:text-2xl">{page.title}</h1>
          {page.created_at ? (
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.page.updated, { time: timeAgo(page.created_at) })}
            </p>
          ) : null}
        </div>

        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
          <section className="rounded-lg border bg-card/30 p-4">
            <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t(($) => $.page.body)}
            </h2>
            {page.content?.trim() ? (
              <div className="prose prose-sm dark:prose-invert max-w-none">
                <Markdown mode="full">{page.content}</Markdown>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">{page.snippet || "—"}</p>
            )}
          </section>

          <aside className="rounded-lg border bg-card/30 p-4">
            <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t(($) => $.page.contacts)}
            </h2>
            <WikiEdgeList
              pageId={page.id}
              edges={neighborEdges}
              loading={neighborsLoading}
            />
          </aside>
        </div>
      </div>
    </div>
  );
}

function StateShell({
  title,
  body,
  onBack,
  backLabel,
  testId,
}: {
  title: string;
  body?: string;
  onBack: () => void;
  backLabel: string;
  testId: string;
}) {
  return (
    <div className="flex h-full flex-col items-start gap-3 px-5 py-8" data-testid={testId}>
      <p className="text-sm font-medium">{title}</p>
      {body ? <p className="text-sm text-muted-foreground">{body}</p> : null}
      <Button type="button" size="sm" variant="outline" onClick={onBack}>
        {backLabel}
      </Button>
    </div>
  );
}
