"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Check, Cloud, Laptop, Loader2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolvePeriodBriefSynthesizerId } from "@multica/core/notes/period-brief-agent";
import {
  defaultPeriodBriefCollectorIds,
  isPeriodBriefCollectorOnline,
  listOwnedPeriodBriefCollectorAgents,
  periodBriefCollectorLabel,
  togglePeriodBriefCollectorId,
} from "@multica/core/notes/period-brief-collectors";
import {
  resolvePeriodBriefComposeRequest,
  type PeriodBriefComposeCollector,
  type PeriodBriefComposeRequest,
  type PeriodBriefComposeSelection,
} from "@multica/core/notes/period-brief-compose";
import {
  defaultPeriodBriefCustomRange,
  isValidPeriodBriefCustomRange,
} from "@multica/core/notes/period-brief-window";
import { runtimeListOptions } from "@multica/core/runtimes";
import type { Agent, NotePeriodBriefWindow } from "@multica/core/types";
import { agentListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";
import { useViewingTimezone } from "../common/use-viewing-timezone";
import { useT } from "../i18n/use-t";

export type NotePeriodBriefResolved = {
  agentId: string | null;
  timezone: string;
  canSubmit: boolean;
  selection: PeriodBriefComposeSelection;
  collectors: PeriodBriefComposeCollector[];
  request: PeriodBriefComposeRequest;
};

const WINDOW_KINDS: NotePeriodBriefWindow[] = ["day", "week", "month", "custom"];

export function NotePeriodBriefCompose({
  active,
  text = "",
  submitting = false,
  startedTitle = null,
  onResolvedChange,
}: {
  active: boolean;
  text?: string;
  submitting?: boolean;
  startedTitle?: string | null;
  onResolvedChange?: (resolved: NotePeriodBriefResolved) => void;
}) {
  const { t } = useT("layout");
  const timezone = useViewingTimezone();
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: Boolean(wsId) && active,
  });
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId),
    enabled: Boolean(wsId) && active,
  });
  const today = useMemo(() => {
    try {
      return new Intl.DateTimeFormat("en-CA", {
        timeZone: timezone,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
      }).format(new Date());
    } catch {
      return new Date().toISOString().slice(0, 10);
    }
  }, [timezone]);
  const [windowKind, setWindowKind] = useState<NotePeriodBriefWindow>("week");
  const defaultCustom = useMemo(() => defaultPeriodBriefCustomRange(today), [today]);
  const [startDate, setStartDate] = useState(defaultCustom.start_date);
  const [endDate, setEndDate] = useState(defaultCustom.end_date);
  const [collectorOverride, setCollectorOverride] = useState<string[] | null>(null);
  const collectorsEnsureAttemptedRef = useRef(false);

  const agentId = resolvePeriodBriefSynthesizerId(agents);
  const collectorAgents = useMemo(
    () => listOwnedPeriodBriefCollectorAgents(agents, runtimes, currentUserId),
    [agents, runtimes, currentUserId],
  );
  const defaultCollectors = useMemo(
    () => defaultPeriodBriefCollectorIds(agents, runtimes, currentUserId),
    [agents, runtimes, currentUserId],
  );
  const collectorIds = collectorOverride ?? defaultCollectors;

  const { mutate: ensurePeriodBriefCollectors, isPending: ensuring } = useMutation({
    mutationFn: (model: string) => api.ensurePeriodBriefCollectors(model),
    onSuccess: (result) => {
      if (!wsId) return;
      queryClient.setQueryData(workspaceKeys.agents(wsId), (current: Agent[] = []) => {
        const byId = new Map(current.map((agent) => [agent.id, agent]));
        for (const agent of result.agents) {
          byId.set(agent.id, agent);
        }
        return [...byId.values()];
      });
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    },
  });

  const prevActiveRef = useRef(active);
  if (active !== prevActiveRef.current) {
    prevActiveRef.current = active;
    if (active) {
      setCollectorOverride(null);
      setWindowKind("week");
      const custom = defaultPeriodBriefCustomRange(today);
      setStartDate(custom.start_date);
      setEndDate(custom.end_date);
      collectorsEnsureAttemptedRef.current = false;
    }
  }

  useEffect(() => {
    if (!active || !wsId || collectorsEnsureAttemptedRef.current) return;
    const ensureModel = agents.find((agent) => agent.model?.trim())?.model?.trim();
    if (!ensureModel) return;
    collectorsEnsureAttemptedRef.current = true;
    ensurePeriodBriefCollectors(ensureModel);
  }, [active, wsId, agents, ensurePeriodBriefCollectors]);

  const collectors = useMemo(
    () =>
      collectorAgents.map((agent) => ({
        id: agent.id,
        label: periodBriefCollectorLabel(agent),
        runtime_mode: agent.runtime_mode,
      })),
    [collectorAgents],
  );
  const selection = useMemo<PeriodBriefComposeSelection>(
    () => ({
      window: windowKind,
      date: today,
      start_date: startDate,
      end_date: endDate,
      collector_ids: collectorIds,
    }),
    [collectorIds, endDate, startDate, today, windowKind],
  );
  const request = useMemo(
    () => resolvePeriodBriefComposeRequest(selection, collectors, text),
    [collectors, selection, text],
  );
  const customRangeValid =
    request.window !== "custom" ||
    isValidPeriodBriefCustomRange(request.start_date ?? "", request.end_date ?? "");
  const canSubmit =
    Boolean(agentId) &&
    request.collector_ids.length > 0 &&
    !submitting &&
    !ensuring &&
    customRangeValid;

  useEffect(() => {
    onResolvedChange?.({
      agentId,
      timezone,
      canSubmit,
      selection,
      collectors,
      request,
    });
  }, [agentId, canSubmit, collectors, onResolvedChange, request, selection, timezone]);

  if (startedTitle) {
    return (
      <div
        className="mx-3 mb-2 rounded-xl border bg-card px-3 py-3 text-sm"
        data-testid="period-brief-started"
      >
        <p className="font-medium">{t(($) => $.notes_page.period_brief_started_title, { title: startedTitle })}</p>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.notes_page.period_brief_started_hint)}
        </p>
      </div>
    );
  }

  const busy = submitting || ensuring;

  return (
    <div className="mx-3 mb-2 space-y-3 rounded-xl border bg-card px-3 py-3" data-testid="period-brief-compose">
      <div className="space-y-1.5">
        <p className="text-xs font-medium text-muted-foreground">
          {t(($) => $.notes_page.period_brief_window_label)}
        </p>
        <div className="flex flex-wrap gap-1.5">
          {WINDOW_KINDS.map((kind) => (
            <Button
              key={kind}
              type="button"
              size="sm"
              variant={windowKind === kind ? "default" : "outline"}
              onClick={() => setWindowKind(kind)}
              disabled={busy}
              data-testid={`period-brief-window-${kind}`}
            >
              {kind === "day"
                ? t(($) => $.notes_page.period_brief_window_day)
                : kind === "week"
                  ? t(($) => $.notes_page.period_brief_window_week)
                  : kind === "month"
                    ? t(($) => $.notes_page.period_brief_window_month)
                    : t(($) => $.notes_page.period_brief_window_custom)}
            </Button>
          ))}
        </div>
      </div>
      {windowKind === "custom" ? (
        <div className="grid grid-cols-2 gap-2">
          <Input
            type="date"
            value={startDate}
            onChange={(event) => setStartDate(event.target.value)}
            disabled={busy}
            aria-label={t(($) => $.notes_page.period_brief_start_date_label)}
            data-testid="period-brief-start-date"
          />
          <Input
            type="date"
            value={endDate}
            onChange={(event) => setEndDate(event.target.value)}
            disabled={busy}
            aria-label={t(($) => $.notes_page.period_brief_end_date_label)}
            data-testid="period-brief-end-date"
          />
        </div>
      ) : null}
      <div className="space-y-1.5">
        <p className="text-xs font-medium text-muted-foreground">
          {t(($) => $.notes_page.period_brief_collectors_label)}
        </p>
        <div className="max-h-36 space-y-1 overflow-y-auto" data-testid="period-brief-collectors">
          {collectorAgents.length === 0 ? (
            <div className="rounded-md border border-dashed px-2 py-2 text-xs text-muted-foreground">
              {ensuring
                ? t(($) => $.notes_page.period_brief_collectors_ensuring)
                : t(($) => $.notes_page.period_brief_collectors_empty)}
            </div>
          ) : (
            collectorAgents.map((agent) => {
              const selected = collectorIds.includes(agent.id);
              const online = isPeriodBriefCollectorOnline(agent, runtimes);
              const name = periodBriefCollectorLabel(agent);
              const isCloud = agent.runtime_mode === "cloud";
              const RuntimeIcon = isCloud ? Cloud : Laptop;
              return (
                <button
                  key={agent.id}
                  type="button"
                  className={cn(
                    "flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm hover:bg-muted/70",
                    selected && "bg-muted text-foreground",
                    !online && "opacity-60",
                  )}
                  onClick={() =>
                    setCollectorOverride((current) =>
                      togglePeriodBriefCollectorId(current ?? defaultCollectors, agent.id),
                    )
                  }
                  disabled={busy}
                  data-testid={`period-brief-collector-${agent.id}`}
                >
                  <RuntimeIcon className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">{name}</span>
                  {!online ? (
                    <span className="shrink-0 text-[10px] text-muted-foreground">
                      {t(($) => $.notes_page.period_brief_collector_offline)}
                    </span>
                  ) : null}
                  {selected ? <Check className="size-3.5 shrink-0 text-primary" /> : null}
                </button>
              );
            })
          )}
        </div>
      </div>
      <p className="text-[11px] leading-4 text-muted-foreground">
        {t(($) => $.notes_page.period_brief_compose_hint)}
      </p>
      {ensuring ? (
        <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <Loader2 className="size-3 animate-spin" />
          {t(($) => $.notes_page.period_brief_collectors_ensuring)}
        </p>
      ) : null}
    </div>
  );
}
