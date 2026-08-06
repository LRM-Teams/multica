"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { BookMarked } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { knowledgeListOptions } from "@multica/core/knowledge";
import { useWorkspacePaths } from "@multica/core/paths";
import { useTimeAgo, useT } from "../../i18n";
import { AppLink, useNavigation } from "../../navigation";
import { PageHeader } from "../../layout/page-header";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { tabToApiKind, type WikiTab } from "../lib/page-kind";

const TAB_QUERY = "tab";

function readTab(searchParams: URLSearchParams): WikiTab {
  const raw = searchParams.get(TAB_QUERY);
  if (raw === "topic" || raw === "decision" || raw === "goal" || raw === "all") return raw;
  return "all";
}

function kindBadgeClass(kind: string): string {
  if (kind === "context") return "bg-sky-500/10 text-sky-700 dark:text-sky-300";
  if (kind === "decision") return "bg-amber-500/10 text-amber-800 dark:text-amber-300";
  if (kind === "goal") return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300";
  return "bg-muted text-muted-foreground";
}

export function KnowledgeListPage() {
  const { t } = useT("knowledge");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { searchParams, replace } = useNavigation();
  const tab = readTab(searchParams);
  const apiKind = tabToApiKind(tab);
  const listEnabled = tab !== "goal";
  const { data, isLoading, isError, error } = useQuery({
    ...knowledgeListOptions(wsId ?? "", apiKind === "goal" ? undefined : apiKind),
    enabled: !!wsId && listEnabled,
  });
  const timeAgo = useTimeAgo();

  const items = useMemo(() => {
    const all = data?.items ?? [];
    if (tab === "all") {
      return all.filter((item) => item.kind === "context" || item.kind === "decision");
    }
    if (tab === "topic") return all.filter((item) => item.kind === "context");
    if (tab === "decision") return all.filter((item) => item.kind === "decision");
    return [];
  }, [data?.items, tab]);

  const setTab = (next: string) => {
    const params = new URLSearchParams(searchParams);
    if (next === "all") params.delete(TAB_QUERY);
    else params.set(TAB_QUERY, next);
    const qs = params.toString();
    replace(`${paths.wiki()}${qs ? `?${qs}` : ""}`);
  };

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="knowledge-list-page">
      <PageHeader className="justify-between px-5">
        <div className="flex min-w-0 items-center gap-2">
          <BookMarked className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
          <p className="ml-2 hidden truncate text-xs text-muted-foreground md:block">
            {t(($) => $.page.tagline)}
          </p>
        </div>
      </PageHeader>

      <div className="border-b px-3 py-2 sm:px-4">
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line" className="h-9 w-full justify-start overflow-x-auto">
            <TabsTrigger value="all">{t(($) => $.page.tabs.all)}</TabsTrigger>
            <TabsTrigger value="topic">{t(($) => $.page.tabs.topic)}</TabsTrigger>
            <TabsTrigger value="decision">{t(($) => $.page.tabs.decision)}</TabsTrigger>
            <TabsTrigger value="goal">{t(($) => $.page.tabs.goal)}</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3 sm:px-5 pb-[max(1rem,env(safe-area-inset-bottom))]">
        {tab === "goal" ? (
          <EmptyState
            title={t(($) => $.page.empty_goal_title)}
            body={t(($) => $.page.empty_goal_body)}
            actionHref={paths.channels()}
            actionLabel={t(($) => $.page.open_channels)}
          />
        ) : isLoading ? (
          <div className="space-y-2" data-testid="knowledge-list-loading">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full rounded-lg" />
            ))}
          </div>
        ) : isError ? (
          <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-6 text-sm">
            {error instanceof Error ? error.message : t(($) => $.page.forbidden)}
          </div>
        ) : items.length === 0 ? (
          <EmptyState
            title={t(($) => $.page.empty_title)}
            body={t(($) => $.page.empty_body)}
          />
        ) : (
          <ul className="space-y-2" data-testid="knowledge-list">
            {items.map((item) => (
              <li key={item.id}>
                <AppLink
                  href={paths.wikiDetail(item.id)}
                  className="block rounded-lg border bg-card/40 px-3 py-3 transition-colors hover:bg-accent/40"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <span
                        className={cn(
                          "inline-flex rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide",
                          kindBadgeClass(item.kind),
                        )}
                      >
                        {item.kind === "context"
                          ? t(($) => $.page.kind.context)
                          : item.kind === "decision"
                            ? t(($) => $.page.kind.decision)
                            : item.kind === "goal"
                              ? t(($) => $.page.kind.goal)
                              : t(($) => $.page.kind.other)}
                      </span>
                      <h2 className="mt-1.5 truncate text-sm font-medium">{item.title || "Untitled"}</h2>
                      {item.snippet ? (
                        <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{item.snippet}</p>
                      ) : null}
                    </div>
                    <time className="shrink-0 text-[11px] text-muted-foreground">
                      {item.created_at
                        ? t(($) => $.page.updated, { time: timeAgo(item.created_at) })
                        : null}
                    </time>
                  </div>
                </AppLink>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function EmptyState({
  title,
  body,
  actionHref,
  actionLabel,
}: {
  title: string;
  body: string;
  actionHref?: string;
  actionLabel?: string;
}) {
  return (
    <div
      className="flex flex-col items-start gap-3 rounded-lg border border-dashed px-4 py-8"
      data-testid="knowledge-list-empty"
    >
      <div>
        <p className="text-sm font-medium">{title}</p>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">{body}</p>
      </div>
      {actionHref && actionLabel ? (
        <AppLink href={actionHref} className={cn(buttonVariants({ size: "sm", variant: "outline" }))}>
          {actionLabel}
        </AppLink>
      ) : null}
    </div>
  );
}
