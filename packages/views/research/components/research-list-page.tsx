"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import {
  researchFleetOptions,
  researchKeys,
  researchSessionListOptions,
} from "@multica/core/research";
import type { ResearchSession } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { AlertCircle, Loader2, SlidersHorizontal } from "lucide-react";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import {
  HERO_COMPOSER_CARD_CLASS,
  HERO_CTA_PRIMARY_CLASS,
  HERO_CTA_SECONDARY_CLASS,
} from "../lib/hero-cta-motion";
import {
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
import { countSessionsByStatus } from "../lib/session-list-counts";
import { RESEARCH_LIST_WORKBENCH_CLASS } from "../lib/research-list-layout";
import { preferredResearchDirectorId } from "../lib/preferred-research-director";
import {
  clearResearchListPersist,
  readResearchListPersist,
  writeResearchListPersist,
} from "../lib/research-list-persist";
import {
  buildCreateGoal,
  composeTemplateGoal,
  composeTemplateStarter,
  localizeTemplateField,
  type ResearchTemplate,
} from "../lib/research-templates";
import { useBrowserOnline } from "../lib/use-browser-online";
import { ResearchConnectivityShell } from "./research-connectivity-shell";
import { ResearchCreateEstimateSummary } from "./research-create-estimate";
import { ResearchCreateParamsPanel } from "./research-create-params-panel";
import { ResearchEmptyState } from "./research-empty-state";
import { ResearchHomeHero } from "./research-home-hero";
import { ResearchHomeOverview } from "./research-home-overview";
import { ResearchHomeConstellationPreview } from "./research-home-constellation-preview";
import { ResearchHomeHeader } from "./research-home-header";
import { ResearchShellAtmosphere } from "./research-shell-atmosphere";
import "./research-home-visual.css";
import { ResearchServerErrorPage } from "./research-server-error-page";
import { ResearchSessionFilterBar } from "./research-session-filter-bar";
import { ResearchSessionRow } from "./research-session-row";
import { ResearchSessionListSkeleton } from "./research-session-row-skeleton";
import { ResearchTemplateChipRow } from "./research-template-chip-row";
import { ResearchTemplateInjectTag } from "./research-template-inject-tag";
import { ResearchTemplatePromptEditor } from "./research-template-prompt-editor";

/**
 * Composer draft — one state object so create/template/goal/params update
 * together (react-doctor prefer-useReducer / related useState).
 */
type ComposerDraft = {
  goal: string;
  /** Last value written programmatically (starter / clear); dirty when goal differs. */
  goalBaseline: string;
  template: ResearchTemplate | null;
  /** LRM-1139 A2: full authoritative prompt (editable via expand); null when no template. */
  templatePrompt: string | null;
  draftTitle: string | undefined;
  params: ResearchCreateParamsDraft;
  paramsOpen: boolean;
  fieldErrors: CreateComposerFieldErrors | null;
  orchestratorVersion: "research-run-v5" | "research-run-v6";
  directorAgentId: string;
};

/**
 * Sentinel for "run without a Director" (legacy V5 fleet). Base UI Select
 * rejects "" as an item value, and the engine version string must never
 * surface in the UI — the user picks a behavior, not an orchestrator tag.
 */
const CLASSIC_FLEET_VALUE = "__classic_fleet__";

function emptyComposer(uiLanguage?: string): ComposerDraft {
  return {
    goal: "",
    goalBaseline: "",
    template: null,
    templatePrompt: null,
    draftTitle: undefined,
    params: defaultCreateParams(uiLanguage),
    paramsOpen: false,
    fieldErrors: null,
    orchestratorVersion: "research-run-v6",
    directorAgentId: "",
  };
}

export function ResearchListPage() {
  const { t, i18n } = useT("research");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const [composer, setComposer] = useState<ComposerDraft>(() => emptyComposer());
  const [titleQuery, setTitleQuery] = useState(
    () => readResearchListPersist()?.q ?? "",
  );
  const [statusFilter, setStatusFilter] = useState<SessionStatusFilter | null>(
    () => readResearchListPersist()?.status ?? null,
  );
  const [promptEditorOpen, setPromptEditorOpen] = useState(false);
  const [createRetrying, setCreateRetrying] = useState(false);
  const [selectedResearchId, setSelectedResearchId] = useState<string | null>(null);
  const goalInputRef = useRef<HTMLTextAreaElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const composerCardRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  /** Scroll offset captured when filters first become active; restored on clear. */
  const savedScrollTop = useRef<number | null>(null);
  const localeSeeded = useRef(false);
  const restoreApplied = useRef(false);
  const pendingFocusSessionId = useRef<string | null>(
    readResearchListPersist()?.sessionId ?? null,
  );
  const lastCreateParamsRef = useRef<ReturnType<typeof normalizeCreateParams> | null>(
    null,
  );
  const lastCreateErrorRef = useRef<string | null>(null);
  const createRequestIdRef = useRef<string | null>(null);

  const {
    goal,
    goalBaseline,
    template: selectedTemplate,
    templatePrompt,
    draftTitle,
    params: createParams,
    paramsOpen,
    fieldErrors,
    orchestratorVersion,
    directorAgentId,
  } = composer;
  const goalDirty = goal !== goalBaseline;
  const defaultTemplatePrompt = selectedTemplate
    ? composeTemplateGoal(selectedTemplate, i18n?.language)
    : "";
  const appliedTemplatePrompt = templatePrompt ?? defaultTemplatePrompt;

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

  // LRM-1115 D-IX: restore scroll after mount (q/status already seeded via useState).
  useEffect(() => {
    if (restoreApplied.current) return;
    restoreApplied.current = true;
    const saved = readResearchListPersist();
    if (!saved?.sessionId) return;
    clearResearchListPersist();
    if (saved.scroll <= 0) return;
    queueMicrotask(() => {
      if (scrollRef.current != null) {
        scrollRef.current.scrollTop = saved.scroll;
      }
    });
  }, []);

  const online = useBrowserOnline();
  const fleetQuery = useQuery(researchFleetOptions(wsId));
  const agentsQuery = useQuery(agentListOptions(wsId));
  const availableDirectors = (agentsQuery.data ?? []).filter(
    (agent) =>
      agent.archived_at == null &&
      Boolean(agent.runtime_id) &&
      agent.runtime_status === "online",
  );
  const selectedDirectorId =
    orchestratorVersion === "research-run-v6"
      ? directorAgentId || preferredResearchDirectorId(availableDirectors)
      : "";
  const selectedDirector = availableDirectors.find(
    (agent) => agent.id === selectedDirectorId,
  );
  const leadLabel =
    orchestratorVersion === "research-run-v5"
      ? t(($) => $.home.lead_fleet_option)
      : selectedDirector
        ? selectedDirector.display_name || selectedDirector.name || selectedDirector.id
        : t(($) => $.d5.rail.director_fallback);
  const { data, isLoading, isFetching, isError, error, refetch } = useQuery(
    researchSessionListOptions(wsId),
  );
  const bootstrapLoading = isLoading || fleetQuery.isLoading;
  const bootstrapError = isError
    ? error
    : fleetQuery.isError
      ? fleetQuery.error
      : null;
  const bootstrapIsError = isError || fleetQuery.isError;
  const bootstrapFetching = isFetching || fleetQuery.isFetching;
  const retryBootstrap = () => {
    void refetch();
    void fleetQuery.refetch();
  };

  const create = useMutation({
    mutationFn: (params: ReturnType<typeof normalizeCreateParams>) => {
      const language = i18n?.language;
      return api.createResearchSession(
        {
          goal: buildCreateGoal(
            selectedTemplate,
            goal,
            language,
            selectedTemplate ? appliedTemplatePrompt : null,
          ),
          depthTier: params.depth_tier,
          language: params.language,
          sourceWeights: params.source_weights,
          ...(draftTitle?.trim() ? { title: draftTitle.trim() } : {}),
          ...(createRequestIdRef.current
            ? { clientRequestId: createRequestIdRef.current }
            : {}),
          ...(orchestratorVersion === "research-run-v6"
            ? { orchestratorVersion, directorAgentId: selectedDirectorId }
            : {}),
        },
        wsId,
      );
    },
    onSuccess: (res) => {
      createRequestIdRef.current = null;
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
        run: res.run,
      });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      nav.push(paths.researchDetail(res.session.id));
    },
  });
  const createBusy = create.isPending || createRetrying;

  const sessions = data?.sessions ?? [];
  const filterActive = isSessionListFilterActive(titleQuery, statusFilter);
  const visibleSessions = filterSessions(sessions, titleQuery, statusFilter);
  const statusCounts = countSessionsByStatus(sessions, titleQuery);
  const inProgress = visibleSessions.filter(
    (s) => !DONE_STATUSES.has(s.status) && !FAILED_STATUSES.has(s.status),
  );
  const completed = visibleSessions.filter((s) => DONE_STATUSES.has(s.status));
  const failed = visibleSessions.filter((s) => FAILED_STATUSES.has(s.status));

  // Group headers only on「全部」with ≥2 non-empty buckets.
  const nonemptyBuckets = [inProgress, completed, failed].filter((b) => b.length > 0);
  const showGroupHeaders = statusFilter == null && nonemptyBuckets.length >= 2;

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

  const focusSearch = () => {
    searchInputRef.current?.focus({ preventScroll: true });
  };

  const clearFilters = () => {
    setTitleQuery("");
    setStatusFilter(null);
    const top = savedScrollTop.current;
    savedScrollTop.current = null;
    clearResearchListPersist();
    queueMicrotask(() => {
      if (scrollRef.current != null && top != null) {
        scrollRef.current.scrollTop = top;
      }
      focusSearch();
    });
  };

  const persistBeforeNavigate = (sessionId: string) => {
    writeResearchListPersist({
      q: titleQuery,
      status: statusFilter,
      scroll: scrollRef.current?.scrollTop ?? 0,
      sessionId,
    });
  };

  // After list paints with restored sessionId, move focus to that row's link.
  useEffect(() => {
    const id = pendingFocusSessionId.current;
    if (!id || isLoading) return;
    const row = scrollRef.current?.querySelector(
      `[data-session-id="${CSS.escape(id)}"] a`,
    ) as HTMLElement | null;
    if (row) {
      pendingFocusSessionId.current = null;
      row.focus({ preventScroll: true });
    }
  }, [isLoading, visibleSessions.length, titleQuery, statusFilter]);

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
      goalBaseline: text,
      template: null,
      templatePrompt: null,
      draftTitle: title,
    }));
    setPromptEditorOpen(false);
    // Defer focus so the controlled value paints before the caret moves.
    queueMicrotask(focusComposer);
  };

  /**
   * LRM-1092 / LRM-1140 A2 / LRM-1139: toggle chip in composer.
   * Select → prefill short starter (skip overwrite when dirty) + seed full prompt.
   * Reselect → clear selection; clear body only when not dirty.
   * Long professional prompts submit via buildCreateGoal(templatePrompt).
   */
  const toggleTemplate = (template: ResearchTemplate) => {
    const language = i18n?.language;
    setComposer((prev) => {
      const dirty = prev.goal !== prev.goalBaseline;
      if (prev.template?.id === template.id) {
        if (dirty) {
          return {
            ...prev,
            template: null,
            templatePrompt: null,
            draftTitle: prev.goal.trim() ? prev.draftTitle : undefined,
          };
        }
        return {
          ...prev,
          goal: "",
          goalBaseline: "",
          template: null,
          templatePrompt: null,
          draftTitle: undefined,
        };
      }
      const starter = composeTemplateStarter(template, language);
      const fullPrompt = composeTemplateGoal(template, language);
      const title = localizeTemplateField(template.sessionTitle, language);
      if (dirty) {
        return {
          ...prev,
          template,
          templatePrompt: fullPrompt,
          draftTitle: title,
        };
      }
      return {
        ...prev,
        goal: starter,
        goalBaseline: starter,
        template,
        templatePrompt: fullPrompt,
        draftTitle: title,
        fieldErrors: prev.fieldErrors?.goal
          ? { ...prev.fieldErrors, goal: undefined }
          : prev.fieldErrors,
      };
    });
    setPromptEditorOpen(false);
    queueMicrotask(focusComposer);
  };

  const submitCreate = () => {
    if (createBusy) return;
    const language = i18n?.language;
    const result = validateCreateComposer({
      goal,
      hasTemplate: Boolean(selectedTemplate),
      params: createParams,
      uiLanguage: language,
    });
    if (orchestratorVersion === "research-run-v6" && !selectedDirectorId) {
      const trigger = document.querySelector(
        '[data-testid="research-create-lead"]',
      );
      if (trigger instanceof HTMLElement) trigger.focus();
      return;
    }
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
    lastCreateParamsRef.current = result.params;
    // A fresh submit is a new intent; retryCreate deliberately keeps this key.
    createRequestIdRef.current = crypto.randomUUID();
    create.mutate(result.params);
  };

  // LRM-787: keep the draft on failure and surface the error inside the card.
  const createErrorBody =
    create.isError &&
    typeof create.error === "object" &&
    create.error != null &&
    "body" in create.error &&
    typeof create.error.body === "object" &&
    create.error.body != null
      ? (create.error.body as { code?: unknown })
      : null;
  const createErrorCode =
    typeof createErrorBody?.code === "string" ? createErrorBody.code : null;
  const localizedCreateError =
    createErrorCode === "research.v6.director_runtime_offline"
      ? t(($) => $.home.director_runtime_offline)
      : createErrorCode === "research.v6.director_runtime_incompatible"
        ? t(($) => $.home.director_runtime_incompatible)
        : createErrorCode === "research.v6.bootstrap_pending"
          ? t(($) => $.home.bootstrap_pending)
          : createErrorCode?.startsWith("research.v6.director_")
            ? t(($) => $.home.director_unavailable)
            : null;
  const createError = create.isError
    ? localizedCreateError ??
      (create.error instanceof Error && create.error.message
        ? create.error.message
        : t(($) => $.home.create_failed))
    : null;
  if (createError) lastCreateErrorRef.current = createError;
  const visibleCreateError =
    createError ?? (createRetrying ? lastCreateErrorRef.current : null);

  const retryCreate = () => {
    if (createBusy) return;
    const params = lastCreateParamsRef.current;
    if (!params) {
      create.reset();
      focusComposer();
      return;
    }
    setCreateRetrying(true);
    create.reset();
    create.mutate(params, {
      onSettled: () => setCreateRetrying(false),
    });
  };

  // Scroll the composer into view once the error banner mounts so the retry is visible.
  useEffect(() => {
    if (!visibleCreateError) return;
    composerCardRef.current?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [visibleCreateError]);

  const filterScopeParts: string[] = [];
  if (titleQuery.trim()) {
    filterScopeParts.push(
      t(($) => $.filter.scope_query, { query: titleQuery.trim() }),
    );
  }
  if (statusFilter === "in_progress") {
    filterScopeParts.push(t(($) => $.filter.status_in_progress));
  } else if (statusFilter === "completed") {
    filterScopeParts.push(t(($) => $.filter.status_completed));
  } else if (statusFilter === "failed") {
    filterScopeParts.push(t(($) => $.filter.status_failed));
  }
  const filterScope = filterScopeParts.join(" · ");

  const renderRow = (s: ResearchSession) => (
    <ResearchSessionRow
      key={s.id}
      session={s}
      href={paths.researchDetail(s.id)}
      onNavigate={() => persistBeforeNavigate(s.id)}
    />
  );

  const renderGroupedOrFlat = () => {
    if (!showGroupHeaders) {
      return <div className="mt-1">{visibleSessions.map(renderRow)}</div>;
    }
    return (
      <div className="space-y-6">
        {inProgress.length > 0 && (
          <section data-testid="research-session-group-in-progress">
            <h2 className="px-3 text-xs font-medium text-muted-foreground">
              {t(($) => $.groups.in_progress)}
              <span className="ml-1.5 tabular-nums font-medium">
                {inProgress.length}
              </span>
            </h2>
            <div className="mt-1">{inProgress.map(renderRow)}</div>
          </section>
        )}
        {completed.length > 0 && (
          <section data-testid="research-session-group-completed">
            <h2 className="px-3 text-xs font-medium text-muted-foreground">
              {t(($) => $.groups.completed)}
              <span className="ml-1.5 tabular-nums font-medium">
                {completed.length}
              </span>
            </h2>
            <div className="mt-1">{completed.map(renderRow)}</div>
          </section>
        )}
        {failed.length > 0 && (
          <section data-testid="research-session-group-failed">
            <h2 className="px-3 text-xs font-medium text-muted-foreground">
              {t(($) => $.filter.status_failed)}
              <span className="ml-1.5 tabular-nums font-medium">
                {failed.length}
              </span>
            </h2>
            <div className="mt-1">{failed.map(renderRow)}</div>
          </section>
        )}
      </div>
    );
  };

  // LRM-833 — 5xx with no cache: dedicated error page + retry (not a blank shell).
  if (
    !bootstrapLoading &&
    !data &&
    bootstrapIsError &&
    isServerError(bootstrapError)
  ) {
    return (
      <ResearchConnectivityShell>
        <ResearchServerErrorPage
          onRetry={retryBootstrap}
          message={bootstrapError instanceof Error ? bootstrapError.message : null}
          retrying={bootstrapFetching}
        />
      </ResearchConnectivityShell>
    );
  }

  return (
    <ResearchConnectivityShell>
      <div
        ref={scrollRef}
        className="dark research-home-theme flex h-full flex-col overflow-y-auto"
        data-testid="research-list-page"
      >
        <div
          className={cn(
            RESEARCH_LIST_WORKBENCH_CLASS,
            "relative flex w-full flex-col gap-4 py-4",
          )}
          data-testid="research-list-workbench"
        >
          {/* LRM-1144 Δ1: dot-grid matches workbench width; omit on skeleton/error. */}
          {!bootstrapLoading && !bootstrapIsError ? (
            <ResearchShellAtmosphere className="-top-2" heightClassName="h-[200px]" />
          ) : null}
          <ResearchHomeHeader sessions={sessions} />
          {/* LRM-783 / LRM-784 / LRM-1106: brand-hero + full-width composer (12 cols). */}
          <div ref={composerCardRef} className="relative z-[1]">
            <ResearchHomeHero preview={<ResearchHomeConstellationPreview sessions={sessions} selectedId={selectedResearchId} />}>
              <div
                className={cn(
                  "w-full overflow-hidden rounded-2xl border bg-card",
                  "focus-within:ring-2 focus-within:ring-ring",
                  HERO_COMPOSER_CARD_CLASS,
                )}
                data-testid="research-home-composer"
              >
                <ResearchTemplateChipRow
                  selectedId={selectedTemplate?.id ?? null}
                  onToggle={toggleTemplate}
                />
                {/* LRM-1138 / LRM-1140 A2: colored inject tag beside short intent. */}
                <div
                  className="flex items-start gap-2 px-3 py-2 md:px-3.5"
                  data-testid="research-composer-intent"
                >
                  {selectedTemplate ? (
                    <ResearchTemplateInjectTag
                      template={selectedTemplate}
                      className="mt-0.5"
                    />
                  ) : null}
                  <Textarea
                    ref={goalInputRef}
                    aria-label={t(($) => $.goal_placeholder)}
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
                      selectedTemplate && !goalDirty
                        ? t(($) => $.home.goal_placeholder_with_template)
                        : t(($) => $.goal_placeholder)
                    }
                    rows={2}
                    aria-invalid={fieldErrors?.goal ? true : undefined}
                    aria-describedby={
                      fieldErrors?.goal ? "research-create-goal-error" : undefined
                    }
                    data-testid="research-create-goal"
                    className="min-h-10 flex-1 border-0 bg-transparent px-0 py-0 text-sm shadow-none focus-visible:ring-0 focus-visible:border-transparent"
                    onKeyDown={(e) => {
                      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                        e.preventDefault();
                        submitCreate();
                      }
                    }}
                  />
                </div>
                {selectedTemplate ? (
                  <div
                    className="flex flex-wrap items-center justify-between gap-2 px-3 pb-2 text-xs text-muted-foreground md:px-3.5"
                    data-testid="research-template-prompt-bar"
                  >
                    <span>{t(($) => $.home.template_injected_hint)}</span>
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto px-0 py-0 text-xs"
                      data-testid="research-template-prompt-edit"
                      onClick={() => setPromptEditorOpen(true)}
                    >
                      {t(($) => $.home.template_prompt_edit)}
                    </Button>
                  </div>
                ) : null}
                {selectedTemplate ? (
                  <ResearchTemplatePromptEditor
                    open={promptEditorOpen}
                    onOpenChange={(open) => {
                      setPromptEditorOpen(open);
                      if (!open) {
                        queueMicrotask(() => {
                          const el = document.querySelector(
                            '[data-testid="research-template-prompt-edit"]',
                          );
                          if (el instanceof HTMLElement) el.focus();
                        });
                      }
                    }}
                    defaultPrompt={defaultTemplatePrompt}
                    value={appliedTemplatePrompt}
                    onApply={(next) =>
                      setComposer((prev) => ({
                        ...prev,
                        templatePrompt: next,
                      }))
                    }
                    disabled={createBusy}
                  />
                ) : null}
                {fieldErrors?.goal ? (
                  <p
                    id="research-create-goal-error"
                    role="alert"
                    data-testid="research-create-goal-error"
                    className="px-3 pb-2 text-xs leading-relaxed text-destructive md:px-3.5"
                  >
                    {t(($) => $.create_params.errors.empty_goal)}
                  </p>
                ) : null}
                <div className="flex flex-col gap-2 border-t px-3 py-2 md:flex-row md:items-center md:justify-between md:gap-3 md:px-3.5">
                  <div className="min-w-0 space-y-0.5">
                    <ResearchCreateEstimateSummary params={createParams} />
                    <p className="hidden text-xs text-muted-foreground md:block">
                      {t(($) => $.home.composer_hint)}
                    </p>
                  </div>
                  <div className="flex w-full flex-col gap-2 md:w-auto md:flex-row md:items-center">
                    {/* Critique 2026-08-21 P0: one behavior-named decision — who
                        hosts the run. Picking an agent runs V6; the classic
                        fleet option maps to V5. Engine strings stay internal. */}
                    <Select
                      value={
                        orchestratorVersion === "research-run-v6"
                          ? selectedDirectorId
                          : CLASSIC_FLEET_VALUE
                      }
                      onValueChange={(next) => {
                        if (createBusy || typeof next !== "string" || next === "") return;
                        setComposer((prev) => ({
                          ...prev,
                          orchestratorVersion:
                            next === CLASSIC_FLEET_VALUE
                              ? "research-run-v5"
                              : "research-run-v6",
                          directorAgentId: next === CLASSIC_FLEET_VALUE ? "" : next,
                        }));
                      }}
                    >
                      <SelectTrigger
                        aria-label={t(($) => $.d5.rail.director_role)}
                        data-testid="research-create-lead"
                        className="h-10 w-full rounded-full px-3.5 text-sm font-medium md:h-8 md:w-auto md:max-w-56"
                      >
                        <span className="shrink-0 text-muted-foreground">
                          {t(($) => $.home.lead_prefix)}
                        </span>
                        <SelectValue>{leadLabel}</SelectValue>
                      </SelectTrigger>
                      <SelectContent align="start">
                        {availableDirectors.map((agent) => (
                          <SelectItem key={agent.id} value={agent.id}>
                            {agent.display_name || agent.name || agent.id}
                          </SelectItem>
                        ))}
                        <SelectItem value={CLASSIC_FLEET_VALUE}>
                          {t(($) => $.home.lead_fleet_option)}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <Button
                      type="button"
                      variant="outline"
                      // LRM-1236 — pending must stay focusable (same root cause as LRM-1213).
                      aria-disabled={createBusy || undefined}
                      onClick={() => {
                        if (createBusy) return;
                        setComposer((prev) => ({ ...prev, paramsOpen: true }));
                      }}
                      className={cn(
                        "h-10 w-full shrink-0 rounded-full px-3.5 text-sm font-medium md:h-8 md:w-auto",
                        HERO_CTA_SECONDARY_CLASS,
                        createBusy && "opacity-50 cursor-not-allowed",
                      )}
                      data-testid="research-create-params-open"
                      aria-label={t(($) => $.create_params.open_aria)}
                    >
                      <SlidersHorizontal className="size-3.5" aria-hidden />
                      {t(($) => $.create_params.open)}
                    </Button>
                    <Button
                      onClick={submitCreate}
                      // LRM-1236 — keep the activated CTA in tab order while mutate is pending.
                      aria-disabled={createBusy || undefined}
                      data-testid="research-create-submit"
                      className={cn(
                        "h-10 w-full shrink-0 rounded-full bg-brand px-4 text-sm font-medium text-brand-foreground md:h-8 md:w-auto",
                        HERO_CTA_PRIMARY_CLASS,
                        createBusy && "opacity-50 cursor-not-allowed",
                      )}
                    >
                      {createBusy ? (
                        <>
                          <Loader2 className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden />
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
                {orchestratorVersion === "research-run-v6" &&
                !agentsQuery.isLoading &&
                availableDirectors.length === 0 ? (
                  <p className="border-t px-3 py-2 text-xs text-destructive md:px-3.5" role="alert">
                    {t(($) => $.home.no_available_director)}
                  </p>
                ) : null}
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
              {visibleCreateError ? (
                <div
                  role="alert"
                  className="mt-2 flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/9 px-3 py-2"
                >
                  <div className="flex min-w-0 items-center gap-2 text-sm text-destructive">
                    <AlertCircle className="size-4 shrink-0" aria-hidden />
                    <span className="truncate">{visibleCreateError}</span>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    aria-disabled={createRetrying || undefined}
                    aria-busy={createRetrying || undefined}
                    className={createRetrying ? "cursor-not-allowed opacity-50" : undefined}
                    onClick={retryCreate}
                  >
                    {t(($) =>
                      createRetrying ? $.connectivity.retrying : $.list.retry,
                    )}
                  </Button>
                </div>
              ) : null}
            </ResearchHomeHero>
          </div>

          {!bootstrapLoading && !bootstrapIsError && sessions.length > 0 ? (
            <ResearchHomeOverview
              sessions={sessions}
              selectedId={selectedResearchId}
              onSelect={setSelectedResearchId}
              hrefFor={(id) => paths.researchDetail(id)}
              onNavigate={persistBeforeNavigate}
            />
          ) : null}

          {bootstrapLoading ? (
            <ResearchSessionListSkeleton rows={4} label={t(($) => $.list.loading)} />
          ) : !data && !online ? (
            <output
              data-testid="research-list-waiting-network"
              className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-warning/35 bg-warning/5 px-6 py-12 text-center"
            >
              <p className="text-sm font-medium text-foreground">
                {t(($) => $.connectivity.waiting_network)}
              </p>
              <p className="max-w-sm text-xs leading-relaxed text-muted-foreground">
                {t(($) => $.connectivity.waiting_network_hint)}
              </p>
            </output>
          ) : bootstrapIsError ? (
            <div
              role="alert"
              data-testid="research-list-error"
              className="flex flex-col items-stretch gap-3 rounded-xl border border-destructive/25 bg-destructive/9 px-4 py-3.5 md:flex-row md:items-center"
            >
              <div className="flex min-w-0 flex-1 items-start gap-3">
                <AlertCircle className="mt-0.5 size-5 shrink-0 text-destructive" />
                <div className="min-w-0">
                  <p className="text-sm font-medium text-foreground">
                    {bootstrapError instanceof Error && bootstrapError.message
                      ? bootstrapError.message
                      : t(($) => $.list.load_failed)}
                  </p>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {t(($) => $.list.load_failed_hint)}
                  </p>
                </div>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                aria-disabled={bootstrapFetching || undefined}
                className={cn(
                  "w-full shrink-0 md:w-auto",
                  bootstrapFetching && "cursor-not-allowed opacity-50",
                )}
                onClick={() => {
                  if (bootstrapFetching) return;
                  retryBootstrap();
                }}
              >
                {t(($) =>
                  bootstrapFetching ? $.connectivity.retrying : $.list.retry,
                )}
              </Button>
            </div>
          ) : sessions.length === 0 ? (
            <ResearchEmptyState
              onSelectExample={fillComposer}
              onStart={focusComposer}
            />
          ) : (
            <div data-testid="research-session-list-content" className="relative z-[1] space-y-3">
              <div className="flex items-baseline gap-2 px-0.5">
                <h2 className="text-sm font-medium text-foreground">
                  {t(($) => $.list.recent_heading)}
                </h2>
                <span className="text-xs tabular-nums text-muted-foreground">
                  {t(($) => $.list.recent_count, { count: sessions.length })}
                </span>
              </div>

              <ResearchSessionFilterBar
                query={titleQuery}
                status={statusFilter}
                counts={statusCounts}
                onQueryChange={setTitleQueryTracked}
                onStatusChange={setStatusFilterTracked}
                onClear={clearFilters}
                searchInputRef={searchInputRef}
              />

              {filterActive && visibleSessions.length > 0 ? (
                <div
                  data-testid="research-filter-scope"
                  className="flex flex-wrap items-center gap-2 px-0.5 text-xs text-muted-foreground"
                >
                  <span>
                    {t(($) => $.filter.scope_line, {
                      scope: filterScope || t(($) => $.filter.status_all),
                      count: visibleSessions.length,
                    })}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs"
                    onClick={clearFilters}
                  >
                    {t(($) => $.filter.clear)}
                  </Button>
                </div>
              ) : null}

              {visibleSessions.length === 0 ? (
                <output
                  aria-live="polite"
                  data-testid="research-filter-no-results"
                  className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed px-6 py-12 text-center"
                >
                  <p className="text-sm font-medium">{t(($) => $.filter.no_results)}</p>
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.filter.no_results_scope, {
                      scope: filterScope || t(($) => $.filter.status_all),
                    })}
                  </p>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={clearFilters}
                    data-testid="research-filter-no-results-clear"
                  >
                    {t(($) => $.filter.clear)}
                  </Button>
                </output>
              ) : (
                renderGroupedOrFlat()
              )}
            </div>
          )}
        </div>
      </div>
    </ResearchConnectivityShell>
  );
}
