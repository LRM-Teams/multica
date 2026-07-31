"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  researchFleetOptions,
  researchKeys,
  researchSessionListOptions,
} from "@multica/core/research";
import type { ResearchSession } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AlertCircle, Loader2, Telescope } from "lucide-react";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import {
  DONE_STATUSES,
  FAILED_STATUSES,
  filterSessions,
  isSessionListFilterActive,
  type SessionStatusFilter,
} from "../lib/session-list-filter";
import { ResearchEmptyState } from "./research-empty-state";
import { ResearchSessionFilterBar } from "./research-session-filter-bar";
import { ResearchSessionRow } from "./research-session-row";

export function ResearchListPage() {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const [goal, setGoal] = useState("");
  const [titleQuery, setTitleQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<SessionStatusFilter | null>(null);
  const goalInputRef = useRef<HTMLTextAreaElement>(null);
  const composerCardRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  /** Scroll offset captured when filters first become active; restored on clear. */
  const savedScrollTop = useRef<number | null>(null);

  useQuery(researchFleetOptions(wsId));
  const { data, isLoading, isError, error, refetch } = useQuery(
    researchSessionListOptions(wsId),
  );

  const create = useMutation({
    mutationFn: () => api.createResearchSession({ goal: goal.trim() }),
    onSuccess: (res) => {
      // Seed snapshot from kickoff payload so the session page paints a busy graph
      // without waiting on the first GET / WS round-trip.
      qc.setQueryData(researchKeys.snapshot(wsId, res.session.id), {
        session: res.session,
        fleet: res.fleet,
        nodes: res.nodes ?? [],
        edges: res.edges ?? [],
        sources: [],
        report: null,
        evals: [],
        messages: res.messages ?? [],
      });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      nav.push(paths.researchDetail(res.session.id));
    },
  });

  const sessions = data?.sessions ?? [];
  const filterActive = isSessionListFilterActive(titleQuery, statusFilter);
  const visibleSessions = filterSessions(sessions, titleQuery, statusFilter);
  const inProgress = visibleSessions.filter(
    (s) => !DONE_STATUSES.has(s.status) && !FAILED_STATUSES.has(s.status),
  );
  const completed = visibleSessions.filter((s) => DONE_STATUSES.has(s.status));
  const failed = visibleSessions.filter((s) => FAILED_STATUSES.has(s.status));

  const rememberScrollIfNeeded = () => {
    if (savedScrollTop.current != null) return;
    savedScrollTop.current = scrollRef.current?.scrollTop ?? 0;
  };

  const setTitleQueryTracked = (value: string) => {
    if (value.trim() || statusFilter) rememberScrollIfNeeded();
    setTitleQuery(value);
  };

  const setStatusFilterTracked = (value: SessionStatusFilter | null) => {
    if (value != null || titleQuery.trim()) rememberScrollIfNeeded();
    setStatusFilter(value);
  };

  const clearFilters = () => {
    setTitleQuery("");
    setStatusFilter(null);
    const top = savedScrollTop.current;
    savedScrollTop.current = null;
    queueMicrotask(() => {
      if (scrollRef.current != null && top != null) {
        scrollRef.current.scrollTop = top;
      }
    });
  };

  const focusComposer = () => {
    const el = goalInputRef.current;
    if (!el) return;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.focus({ preventScroll: true });
  };

  const fillComposer = (text: string) => {
    setGoal(text);
    // Defer focus so the controlled value paints before the caret moves.
    queueMicrotask(focusComposer);
  };

  const submitCreate = () => {
    const value = goal.trim();
    if (!value || create.isPending) return;
    create.mutate();
  };

  // LRM-787: keep the draft on failure and surface the error inside the card.
  const createError =
    create.isError && create.error instanceof Error && create.error.message
      ? create.error.message
      : create.isError
        ? t(($) => $.home.create_failed)
        : null;

  const retryCreate = () => {
    create.reset();
    focusComposer();
  };

  // Scroll the composer into view once the error banner mounts so the retry is visible.
  useEffect(() => {
    if (!createError) return;
    composerCardRef.current?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [createError]);

  const renderRow = (s: ResearchSession) => (
    <ResearchSessionRow key={s.id} session={s} href={paths.researchDetail(s.id)} />
  );

  return (
    <div ref={scrollRef} className="flex h-full flex-col gap-6 overflow-y-auto p-6">
      {/* LRM-787: hero composer card — brand presence, focus ring, inline failure. */}
      <section
        ref={composerCardRef}
        aria-label={t(($) => $.home.composer_label)}
        className="relative w-full max-w-3xl overflow-hidden rounded-2xl border bg-card shadow-sm"
      >
        <div className="pointer-events-none absolute inset-0 bg-brand/4" aria-hidden />
        <div className="relative flex flex-col gap-4 p-6 sm:p-8">
          <div className="flex items-start gap-3">
            <div className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-brand/10 text-brand">
              <Telescope className="size-5" aria-hidden />
            </div>
            <div className="min-w-0 space-y-1">
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
                {t(($) => $.home.hero_title)}
              </h1>
              <p className="text-sm text-muted-foreground sm:text-base">
                {t(($) => $.home.hero_desc)}
              </p>
            </div>
          </div>

          <div
            className={
              // Brand focus ring without a new hex token; 22% mix ≈ ring brand/22.
              "rounded-xl border bg-background transition-shadow focus-within:border-brand/40 focus-within:ring-3 focus-within:ring-brand/22"
            }
          >
            <Textarea
              ref={goalInputRef}
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
              placeholder={t(($) => $.goal_placeholder)}
              rows={4}
              className="border-0 bg-transparent shadow-none focus-visible:ring-0 focus-visible:border-transparent"
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                  e.preventDefault();
                  submitCreate();
                }
              }}
            />
            <div className="flex items-center justify-between gap-3 border-t px-3 py-2">
              <p className="text-xs text-muted-foreground">
                {t(($) => $.home.composer_hint)}
              </p>
              <Button
                onClick={submitCreate}
                disabled={!goal.trim() || create.isPending}
                className="shrink-0"
              >
                {create.isPending ? (
                  <>
                    <Loader2 className="size-4 animate-spin" aria-hidden />
                    {t(($) => $.home.creating)}
                  </>
                ) : (
                  t(($) => $.start)
                )}
              </Button>
            </div>
          </div>

          {createError ? (
            <div
              role="alert"
              className="flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/9 px-3 py-2"
            >
              <div className="flex min-w-0 items-center gap-2 text-sm text-destructive">
                <AlertCircle className="size-4 shrink-0" aria-hidden />
                <span className="truncate">{createError}</span>
              </div>
              <Button variant="outline" size="sm" onClick={retryCreate}>
                {t(($) => $.list.retry)}
              </Button>
            </div>
          ) : null}
        </div>
      </section>

      {isLoading ? (
        <div className="space-y-2" aria-busy="true" aria-label={t(($) => $.list.loading)}>
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : isError ? (
        <div
          role="alert"
          className="flex flex-col items-center justify-center gap-3 rounded-lg border border-destructive/40 bg-destructive/5 px-6 py-12 text-center"
        >
          <AlertCircle className="size-6 text-destructive" />
          <p className="text-sm text-destructive">
            {error instanceof Error && error.message
              ? error.message
              : t(($) => $.list.load_failed)}
          </p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            {t(($) => $.list.retry)}
          </Button>
        </div>
      ) : sessions.length === 0 ? (
        <ResearchEmptyState
          onSelectExample={fillComposer}
          onStart={focusComposer}
        />
      ) : (
        <div className="space-y-4">
          <ResearchSessionFilterBar
            query={titleQuery}
            status={statusFilter}
            active={filterActive}
            onQueryChange={setTitleQueryTracked}
            onStatusChange={setStatusFilterTracked}
            onClear={clearFilters}
          />
          {visibleSessions.length === 0 ? (
            <output className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed px-6 py-12 text-center">
              <p className="text-sm font-medium">{t(($) => $.filter.no_results)}</p>
              <p className="text-xs text-muted-foreground">
                {t(($) => $.filter.no_results_hint)}
              </p>
              <Button variant="outline" size="sm" onClick={clearFilters}>
                {t(($) => $.filter.clear)}
              </Button>
            </output>
          ) : (
            <div className="space-y-6">
              {inProgress.length > 0 && (
                <section>
                  <h2 className="px-1 text-xs font-medium text-muted-foreground">
                    {t(($) => $.groups.in_progress)}
                  </h2>
                  <div className="mt-2 space-y-2">{inProgress.map(renderRow)}</div>
                </section>
              )}
              {completed.length > 0 && (
                <section>
                  <h2 className="px-1 text-xs font-medium text-muted-foreground">
                    {t(($) => $.groups.completed)}
                  </h2>
                  <div className="mt-2 space-y-2">{completed.map(renderRow)}</div>
                </section>
              )}
              {failed.length > 0 && (
                <section>
                  <h2 className="px-1 text-xs font-medium text-muted-foreground">
                    {t(($) => $.filter.status_failed)}
                  </h2>
                  <div className="mt-2 space-y-2">{failed.map(renderRow)}</div>
                </section>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
