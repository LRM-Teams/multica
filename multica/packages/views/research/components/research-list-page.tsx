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
import { cn } from "@multica/ui/lib/utils";
import { AlertCircle, Loader2, SlidersHorizontal, X } from "lucide-react";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import {
  HERO_COMPOSER_CARD_CLASS,
  HERO_CTA_PRIMARY_CLASS,
  HERO_CTA_SECONDARY_CLASS,
} from "../lib/hero-cta-motion";
import {
  appendCreateParamsToGoal,
  defaultCreateParams,
  draftCreateParams,
  normalizeCreateParams,
  validateCreateComposer,
  type CreateComposerFieldErrors,
  type ResearchCreateParamsDraft,
} from "../lib/research-create-params";
import { isServerError } from "../lib/network-status";
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
import { useBrowserOnline } from "../lib/use-browser-online";
import { ResearchConnectivityShell } from "./research-connectivity-shell";
import { ResearchCreateEstimateSummary } from "./research-create-estimate";
import { ResearchCreateParamsPanel } from "./research-create-params-panel";
import { ResearchEmptyState } from "./research-empty-state";
import { ResearchHomeHero } from "./research-home-hero";
import { ResearchServerErrorPage } from "./research-server-error-page";
import { ResearchSessionFilterBar } from "./research-session-filter-bar";
import { ResearchSessionRow } from "./research-session-row";
import { ResearchSessionListSkeleton } from "./research-session-row-skeleton";
import { ResearchTemplateCards } from "./research-template-cards";

/**
 * Composer draft — one state object so create/template/goal/params update
 * together (react-doctor prefer-useReducer / related useState).
 */
type ComposerDraft = {
  goal: string;
  template: ResearchTemplate | null;
  draftTitle: string | undefined;
  params: ResearchCreateParamsDraft;
  paramsOpen: boolean;
  fieldErrors: CreateComposerFieldErrors | null;
};

function emptyComposer(uiLanguage?: string): ComposerDraft {
  return {
    goal: "",
    template: null,
    draftTitle: undefined,
    params: defaultCreateParams(uiLanguage),
    paramsOpen: false,
    fieldErrors: null,
  };
}

export function ResearchListPage() {
  const { t, i18n } = useT("research");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const [composer, setComposer] = useState<ComposerDraft>(() => emptyComposer());
  const [titleQuery, setTitleQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<SessionStatusFilter | null>(null);
  const goalInputRef = useRef<HTMLTextAreaElement>(null);
  const composerCardRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  /** Scroll offset captured when filters first become active; restored on clear. */
  const savedScrollTop = useRef<number | null>(null);
  const localeSeeded = useRef(false);

  const {
    goal,
    template: selectedTemplate,
    draftTitle,
    params: createParams,
    paramsOpen,
    fieldErrors,
  } = composer;

  // Seed language from UI locale once i18n is ready (defaults stay standard/weights).
  useEffect(() => {
    if (localeSeeded.current || !i18n?.language) return;
    localeSeeded.current = true;
    setComposer((prev) => ({
      ...prev,
      params: draftCreateParams(
        { ...prev.params, language: undefined },
        i18n.language,
      ),
    }));
  }, [i18n?.language]);

  const online = useBrowserOnline();
  useQuery(researchFleetOptions(wsId));
  const { data, isLoading, isFetching, isError, error, refetch } = useQuery(
    researchSessionListOptions(wsId),
  );

  const create = useMutation({
    mutationFn: (params: ReturnType<typeof normalizeCreateParams>) => {
      const language = i18n?.language;
      const mergedGoal = appendCreateParamsToGoal(
        buildCreateGoal(selectedTemplate, goal, language),
        params,
      );
      return api.createResearchSession({
        goal: mergedGoal,
        depth_tier: params.depth_tier,
        language: params.language,
        source_weights: params.source_weights,
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
    setComposer((prev) => ({
      ...prev,
      goal: text,
      template: null,
      draftTitle: title,
    }));
    // Defer focus so the controlled value paints before the caret moves.
    queueMicrotask(focusComposer);
  };

  /** LRM-906 T2: chip only — never dump ≥800-char professional prompt into the box. */
  const applyTemplate = (template: ResearchTemplate) => {
    const language = i18n?.language;
    setComposer((prev) => ({
      ...prev,
      goal: "",
      template,
      draftTitle: localizeTemplateField(template.sessionTitle, language),
    }));
    queueMicrotask(focusComposer);
  };

  const clearTemplate = () => {
    setComposer((prev) => ({
      ...prev,
      template: null,
      draftTitle: prev.goal.trim() ? prev.draftTitle : undefined,
    }));
  };

  const submitCreate = () => {
    if (create.isPending) return;
    const language = i18n?.language;
    const result = validateCreateComposer({
      goal,
      hasTemplate: Boolean(selectedTemplate),
      params: createParams,
      uiLanguage: language,
    });
    if (!result.ok) {
      // Keep draft (goal / depth / weights / language); surface near-field errors.
      const openParams = Boolean(result.errors.depth || result.errors.weights);
      setComposer((prev) => ({
        ...prev,
        fieldErrors: result.errors,
        paramsOpen: openParams ? true : prev.paramsOpen,
      }));
      if (result.errors.goal) {
        queueMicrotask(focusComposer);
      }
      return;
    }
    setComposer((prev) => ({
      ...prev,
      fieldErrors: null,
      params: result.params,
    }));
    create.mutate(result.params);
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

  // LRM-833 — 5xx with no cache: dedicated error page + retry (not a blank shell).
  if (!isLoading && !data && isError && isServerError(error)) {
    return (
      <ResearchConnectivityShell>
        <ResearchServerErrorPage
          onRetry={() => {
            void refetch();
          }}
          message={error instanceof Error ? error.message : null}
          retrying={isFetching}
        />
      </ResearchConnectivityShell>
    );
  }

  return (
    <ResearchConnectivityShell>
    <div
      ref={scrollRef}
      className="flex h-full flex-col gap-5 overflow-y-auto p-4 sm:gap-6 sm:p-6"
      data-testid="research-list-page"
    >
      {/* LRM-783 / LRM-784: brand-hero façade + composer (not bare h1 + gray box). */}
      <div ref={composerCardRef}>
        <ResearchHomeHero>
          <div
            className={cn(
              "overflow-hidden rounded-2xl border bg-card shadow-sm",
              HERO_COMPOSER_CARD_CLASS,
            )}
            data-testid="research-home-composer"
          >
            {selectedTemplate ? (
              <div className="flex flex-wrap gap-1.5 px-3 pt-2.5 sm:px-3.5">
                <span className="inline-flex max-w-full items-center gap-1.5 rounded-full border border-brand/25 bg-brand/8 px-2.5 py-1 text-[11px] font-semibold text-brand">
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
                setComposer((prev) => ({
                  ...prev,
                  goal: e.target.value,
                  fieldErrors: prev.fieldErrors?.goal
                    ? { ...prev.fieldErrors, goal: undefined }
                    : prev.fieldErrors,
                }))
              }
              placeholder={
                selectedTemplate
                  ? t(($) => $.home.goal_placeholder_with_template)
                  : t(($) => $.goal_placeholder)
              }
              rows={2}
              aria-invalid={fieldErrors?.goal ? true : undefined}
              aria-describedby={
                fieldErrors?.goal ? "research-create-goal-error" : undefined
              }
              data-testid="research-create-goal"
              className="min-h-[64px] border-0 bg-transparent px-3 py-3 text-[13.5px] shadow-none focus-visible:ring-0 focus-visible:border-transparent sm:px-3.5"
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                  e.preventDefault();
                  submitCreate();
                }
              }}
            />
            {fieldErrors?.goal ? (
              <p
                id="research-create-goal-error"
                role="alert"
                data-testid="research-create-goal-error"
                className="px-3 pb-2 text-[12px] leading-relaxed text-destructive sm:px-3.5"
              >
                {t(($) => $.create_params.errors.empty_goal)}
              </p>
            ) : null}
            <div className="flex flex-col gap-2 border-t px-3 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3 sm:px-3.5">
              {/* LRM-839: estimate line; LRM-790 ⌘ hint yields on narrow. */}
              <div className="min-w-0 space-y-0.5">
                <ResearchCreateEstimateSummary params={createParams} />
                <p className="hidden text-xs text-muted-foreground sm:block">
                  {t(($) => $.home.composer_hint)}
                </p>
              </div>
              <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() =>
                    setComposer((prev) => ({ ...prev, paramsOpen: true }))
                  }
                  disabled={create.isPending}
                  className={cn(
                    "h-10 w-full shrink-0 rounded-full px-3.5 text-[13px] font-medium sm:h-9 sm:w-auto",
                    HERO_CTA_SECONDARY_CLASS,
                  )}
                  data-testid="research-create-params-open"
                  aria-label={t(($) => $.create_params.open_aria)}
                >
                  <SlidersHorizontal className="size-3.5" aria-hidden />
                  {t(($) => $.create_params.open)}
                </Button>
                <Button
                  onClick={submitCreate}
                  disabled={create.isPending}
                  data-testid="research-create-submit"
                  className={cn(
                    "h-10 w-full shrink-0 rounded-full bg-brand px-4 text-[13.5px] font-semibold text-brand-foreground sm:h-9 sm:w-auto",
                    HERO_CTA_PRIMARY_CLASS,
                  )}
                >
                  {create.isPending ? (
                    <>
                      <Loader2 className="size-3.5 animate-spin" aria-hidden />
                      {t(($) => $.home.creating)}
                    </>
                  ) : (
                    <>
                      {t(($) => $.start)}
                      <span aria-hidden className="ml-0.5">
                        →
                      </span>
                    </>
                  )}
                </Button>
              </div>
            </div>
          </div>

          <ResearchCreateParamsPanel
            open={paramsOpen}
            value={createParams}
            errors={fieldErrors}
            onOpenChange={(open) =>
              setComposer((prev) => ({ ...prev, paramsOpen: open }))
            }
            onChange={(params) =>
              setComposer((prev) => ({ ...prev, params }))
            }
            onErrorsChange={(next) =>
              setComposer((prev) => ({
                ...prev,
                fieldErrors: next
                  ? { ...prev.fieldErrors, ...next, goal: prev.fieldErrors?.goal }
                  : prev.fieldErrors?.goal
                    ? { goal: prev.fieldErrors.goal }
                    : null,
              }))
            }
          />

          {createError ? (
            <div
              role="alert"
              className="mt-2 flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/9 px-3 py-2"
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
        </ResearchHomeHero>
      </div>

      {/* LRM-817: quick template cards — chip injection (LRM-906 T2). */}
      <ResearchTemplateCards onSelect={applyTemplate} />

      {isLoading ? (
        <ResearchSessionListSkeleton rows={4} label={t(($) => $.list.loading)} />
      ) : !data && !online ? (
        <output
          data-testid="research-list-waiting-network"
          className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-warning/35 bg-warning/5 px-6 py-12 text-center"
        >
          <p className="text-sm font-semibold text-foreground">
            {t(($) => $.connectivity.waiting_network)}
          </p>
          <p className="max-w-sm text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.connectivity.waiting_network_hint)}
          </p>
        </output>
      ) : isError ? (
        <div
          role="alert"
          data-testid="research-list-error"
          className="flex flex-col items-stretch gap-3 rounded-xl border border-destructive/25 bg-destructive/9 px-4 py-3.5 sm:flex-row sm:items-center"
        >
          <div className="flex min-w-0 flex-1 items-start gap-3">
            <AlertCircle className="mt-0.5 size-5 shrink-0 text-destructive" />
            <div className="min-w-0">
              <p className="text-sm font-semibold text-foreground">
                {error instanceof Error && error.message
                  ? error.message
                  : t(($) => $.list.load_failed)}
              </p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {t(($) => $.list.load_failed_hint)}
              </p>
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="w-full shrink-0 sm:w-auto"
            onClick={() => refetch()}
          >
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
                  <div className="mt-1">{inProgress.map(renderRow)}</div>
                </section>
              )}
              {completed.length > 0 && (
                <section>
                  <h2 className="px-3 text-xs font-semibold text-muted-foreground">
                    {t(($) => $.groups.completed)}
                  </h2>
                  <div className="mt-1">{completed.map(renderRow)}</div>
                </section>
              )}
              {failed.length > 0 && (
                <section>
                  <h2 className="px-3 text-xs font-semibold text-muted-foreground">
                    {t(($) => $.filter.status_failed)}
                  </h2>
                  <div className="mt-1">{failed.map(renderRow)}</div>
                </section>
              )}
            </div>
          )}
        </div>
      )}
    </div>
    </ResearchConnectivityShell>
  );
}
