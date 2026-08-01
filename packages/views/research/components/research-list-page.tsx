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
import { AlertCircle, ArrowRight, Loader2, X } from "lucide-react";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import {
  DONE_STATUSES,
  FAILED_STATUSES,
  filterSessions,
  isSessionListFilterActive,
  type SessionStatusFilter,
} from "../lib/session-list-filter";
import {
  buildCreateGoal,
  localizeTemplateField,
  type ResearchTemplate,
} from "../lib/research-templates";
import { ResearchEmptyState } from "./research-empty-state";
import { ResearchSessionFilterBar } from "./research-session-filter-bar";
import { ResearchSessionRow } from "./research-session-row";
import { ResearchSessionListSkeleton } from "./research-session-row-skeleton";
import { ResearchTemplateCards } from "./research-template-cards";

/** Composer draft — one state object so create/template/goal update together (react-doctor). */
type ComposerDraft = {
  goal: string;
  template: ResearchTemplate | null;
  draftTitle: string | undefined;
};

const EMPTY_COMPOSER: ComposerDraft = {
  goal: "",
  template: null,
  draftTitle: undefined,
};

function ResearchCompassGlyph({ className }: { className?: string }) {
  return (
    <svg
      className={className}
      width="19"
      height="19"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden
    >
      <circle
        cx="12"
        cy="12"
        r="9"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <path d="M15.5 8.5 13 13l-4.5 2.5L11 11z" fill="currentColor" />
    </svg>
  );
}

export function ResearchListPage() {
  const { t, i18n } = useT("research");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const [composer, setComposer] = useState<ComposerDraft>(EMPTY_COMPOSER);
  const [titleQuery, setTitleQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<SessionStatusFilter | null>(null);
  const goalInputRef = useRef<HTMLTextAreaElement>(null);
  const composerCardRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  /** Scroll offset captured when filters first become active; restored on clear. */
  const savedScrollTop = useRef<number | null>(null);

  const { goal, template: selectedTemplate, draftTitle } = composer;

  useQuery(researchFleetOptions(wsId));
  const { data, isLoading, isError, error, refetch } = useQuery(
    researchSessionListOptions(wsId),
  );

  const create = useMutation({
    mutationFn: () => {
      const language = i18n?.language;
      const mergedGoal = buildCreateGoal(selectedTemplate, goal, language);
      return api.createResearchSession({
        goal: mergedGoal,
        ...(draftTitle?.trim() ? { title: draftTitle.trim() } : {}),
      });
    },
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

  const fillComposer = (text: string, title?: string) => {
    setComposer({ goal: text, template: null, draftTitle: title });
    // Defer focus so the controlled value paints before the caret moves.
    queueMicrotask(focusComposer);
  };

  /** LRM-906 T2: chip only — never dump ≥800-char professional prompt into the box. */
  const applyTemplate = (template: ResearchTemplate) => {
    const language = i18n?.language;
    setComposer({
      goal: "",
      template,
      draftTitle: localizeTemplateField(template.sessionTitle, language),
    });
    queueMicrotask(focusComposer);
  };

  const clearTemplate = () => {
    setComposer((prev) => ({
      ...prev,
      template: null,
      draftTitle: prev.goal.trim() ? prev.draftTitle : undefined,
    }));
  };

  const canSubmit = Boolean(selectedTemplate) || Boolean(goal.trim());

  const submitCreate = () => {
    if (!canSubmit || create.isPending) return;
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

  const templateTitle = selectedTemplate
    ? localizeTemplateField(selectedTemplate.title, i18n?.language)
    : "";

  return (
    <div ref={scrollRef} className="relative h-full overflow-y-auto">
      {/* LRM-784 / LRM-785: hero-zone dot grid (canvas chrome extension, fades down). */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-[340px]"
        style={{
          backgroundImage:
            "radial-gradient(circle, color-mix(in oklab, var(--foreground) 9%, transparent) 1px, transparent 1.5px)",
          backgroundSize: "24px 24px",
          WebkitMaskImage: "linear-gradient(to bottom, black 0%, transparent 92%)",
          maskImage: "linear-gradient(to bottom, black 0%, transparent 92%)",
        }}
      />

      <div className="relative mx-auto flex w-full max-w-3xl flex-col gap-5 px-4 py-8 sm:px-6 sm:py-11">
        {/* Facade: brand-level title + one value line (LRM-783 AC1). */}
        <header className="relative">
          <div className="flex items-center gap-2.5">
            <div className="flex size-[34px] shrink-0 items-center justify-center rounded-[9px] bg-brand/10 text-brand">
              <ResearchCompassGlyph />
            </div>
            <h1 className="text-[22px] font-semibold tracking-tight sm:text-[26px]">
              {t(($) => $.home.hero_title)}
            </h1>
          </div>
          <p className="mt-2 max-w-xl text-[13px] leading-relaxed text-muted-foreground sm:text-[13.5px]">
            {t(($) => $.home.hero_desc)}
          </p>
        </header>

        <section
          ref={composerCardRef}
          aria-label={t(($) => $.home.composer_label)}
          className="rounded-2xl border bg-card shadow-sm transition-[border-color,box-shadow] focus-within:border-brand focus-within:shadow-[0_0_0_3px_color-mix(in_oklab,var(--brand)_22%,transparent)]"
        >
          <div className="flex flex-col">
            {selectedTemplate ? (
              <div className="flex flex-wrap gap-1.5 px-4 pt-3.5">
                <span className="inline-flex max-w-full items-center gap-1.5 rounded-full border border-violet-500/30 bg-gradient-to-r from-violet-500/10 to-brand/10 px-2.5 py-1 text-[11px] font-semibold text-violet-700 dark:text-violet-300">
                  <span className="truncate">
                    {t(($) => $.home.template_chip, { title: templateTitle })}
                  </span>
                  <button
                    type="button"
                    className="shrink-0 rounded-full p-0.5 opacity-60 hover:opacity-100"
                    aria-label={t(($) => $.home.template_chip_clear)}
                    onClick={clearTemplate}
                  >
                    <X className="size-3" aria-hidden />
                  </button>
                </span>
              </div>
            ) : null}
            <Textarea
              ref={goalInputRef}
              value={goal}
              onChange={(e) =>
                setComposer((prev) => ({ ...prev, goal: e.target.value }))
              }
              placeholder={
                selectedTemplate
                  ? t(($) => $.home.goal_placeholder_with_template)
                  : t(($) => $.goal_placeholder)
              }
              rows={2}
              className="min-h-[64px] border-0 bg-transparent px-4 pt-3.5 pb-2.5 text-[15px] leading-relaxed shadow-none focus-visible:ring-0 focus-visible:border-transparent"
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                  e.preventDefault();
                  submitCreate();
                }
              }}
            />
            <div className="flex flex-col gap-2 px-3 pb-3 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
              <p className="hidden text-xs text-muted-foreground sm:block">
                {t(($) => $.home.composer_hint)}
              </p>
              <Button
                onClick={submitCreate}
                disabled={!canSubmit || create.isPending}
                className="h-10 w-full shrink-0 rounded-full bg-brand text-brand-foreground hover:bg-brand/90 sm:h-9 sm:w-auto"
              >
                {create.isPending ? (
                  <>
                    <Loader2 className="size-4 animate-spin" aria-hidden />
                    {t(($) => $.home.creating)}
                  </>
                ) : (
                  <>
                    {t(($) => $.start)}
                    <ArrowRight className="size-3.5" aria-hidden />
                  </>
                )}
              </Button>
            </div>

            {createError ? (
              <div
                role="alert"
                className="mx-3 mb-3 flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/9 px-3 py-2"
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

        {/* LRM-817: quick template cards — chip injection (LRM-906 T2). */}
        <ResearchTemplateCards onSelect={applyTemplate} />

        {isLoading ? (
          <ResearchSessionListSkeleton rows={4} label={t(($) => $.list.loading)} />
        ) : isError ? (
          <div
            role="alert"
            className="flex items-start gap-3 rounded-xl border border-destructive/25 bg-destructive/9 px-4 py-3.5"
          >
            <AlertCircle className="mt-0.5 size-5 shrink-0 text-destructive" />
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold">
                {error instanceof Error && error.message
                  ? error.message
                  : t(($) => $.list.load_failed)}
              </p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {t(($) => $.list.load_failed_hint)}
              </p>
            </div>
            <Button variant="outline" size="sm" className="shrink-0" onClick={() => refetch()}>
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
                    <h2 className="px-3 text-xs font-semibold text-muted-foreground">
                      {t(($) => $.groups.in_progress)}
                    </h2>
                    <div className="mt-1 flex flex-col">{inProgress.map(renderRow)}</div>
                  </section>
                )}
                {completed.length > 0 && (
                  <section>
                    <h2 className="px-3 text-xs font-semibold text-muted-foreground">
                      {t(($) => $.groups.completed)}
                    </h2>
                    <div className="mt-1 flex flex-col">{completed.map(renderRow)}</div>
                  </section>
                )}
                {failed.length > 0 && (
                  <section>
                    <h2 className="px-3 text-xs font-semibold text-muted-foreground">
                      {t(($) => $.filter.status_failed)}
                    </h2>
                    <div className="mt-1 flex flex-col">{failed.map(renderRow)}</div>
                  </section>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
