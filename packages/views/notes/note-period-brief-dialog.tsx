/**
 * @vitest-environment happy-dom
 */
"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Check, ClipboardList, Cloud, Laptop, Loader2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  PERIOD_BRIEF_AGENT_DISPLAY_NAME,
  isPeriodBriefAgent,
  resolvePeriodBriefSynthesizerId,
} from "@multica/core/notes/period-brief-agent";
import {
  listPeriodBriefCollectorAgents,
  defaultPeriodBriefCollectorIds,
  isPeriodBriefCollectorOnline,
  periodBriefCollectorLabel,
  togglePeriodBriefCollectorId,
} from "@multica/core/notes/period-brief-collectors";
import { runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type {
  Agent,
  CreateNotePeriodBriefResponse,
  NoteRetrospectiveWindow,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../i18n/use-t";
import { useViewingTimezone } from "../common/use-viewing-timezone";
import { useOpenNoteWorkerChat } from "./use-open-note-worker-chat";

export function NotePeriodBriefDialog({
  open,
  onOpenChange,
  preferredAgentId = null,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  preferredAgentId?: string | null;
  onCreated?: (result: CreateNotePeriodBriefResponse) => void;
}) {
  const { t } = useT("layout");
  const timezone = useViewingTimezone();
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const { openNoteWorkerChat } = useOpenNoteWorkerChat();
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId),
    enabled: Boolean(wsId) && open,
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
  const [windowKind, setWindowKind] = useState<NoteRetrospectiveWindow>("week");
  const [date, setDate] = useState(today);
  /** null = follow derived default; set once the user (or open reset) pins a choice. */
  const [agentOverride, setAgentOverride] = useState<string | null>(null);
  const [collectorOverride, setCollectorOverride] = useState<string[] | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const ensureAttemptedRef = useRef(false);
  const collectorsEnsureAttemptedRef = useRef(false);

  const resolvedPreferredAgentId = resolvePeriodBriefSynthesizerId(agents, preferredAgentId);
  const collectorAgents = useMemo(() => listPeriodBriefCollectorAgents(agents), [agents]);
  const defaultCollectors = useMemo(
    () => defaultPeriodBriefCollectorIds(agents, runtimes),
    [agents, runtimes],
  );
  const agentId = agentOverride ?? resolvedPreferredAgentId;
  const collectorIds = collectorOverride ?? defaultCollectors;

  const { mutate: ensurePeriodBriefAgent, isPending: ensuringAgent } = useMutation({
    mutationFn: ({ runtimeId, model }: { runtimeId: string; model: string }) =>
      api.ensurePeriodBriefAgent(runtimeId, model),
    onSuccess: (result) => {
      if (!wsId) return;
      queryClient.setQueryData(workspaceKeys.agents(wsId), (current: Agent[] = []) => {
        if (current.some((agent) => agent.id === result.agent.id)) return current;
        return [...current, result.agent];
      });
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    },
  });

  const { mutate: ensurePeriodBriefCollectors, isPending: ensuringCollectors } = useMutation({
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

  const ensuring = ensuringAgent || ensuringCollectors;

  // Reset form fields when the dialog opens — adjust during render (prev ref).
  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      setAgentOverride(null);
      setCollectorOverride(null);
      setWindowKind("week");
      setDate(today);
      setSubmitting(false);
      ensureAttemptedRef.current = false;
      collectorsEnsureAttemptedRef.current = false;
    }
  }

  useEffect(() => {
    if (!open || !wsId || ensureAttemptedRef.current) return;
    if (agents.some((agent) => isPeriodBriefAgent(agent))) return;
    const donor = agents.find((agent) => agent.runtime_id && agent.model?.trim());
    const runtimeId = donor?.runtime_id ?? runtimes[0]?.id;
    const model = donor?.model?.trim();
    if (!runtimeId || !model) return;
    ensureAttemptedRef.current = true;
    ensurePeriodBriefAgent({ runtimeId, model });
  }, [open, wsId, agents, runtimes, ensurePeriodBriefAgent]);

  useEffect(() => {
    if (!open || !wsId || collectorsEnsureAttemptedRef.current) return;
    const ensureModel = agents.find((agent) => agent.model?.trim())?.model?.trim();
    if (!ensureModel) return;
    collectorsEnsureAttemptedRef.current = true;
    ensurePeriodBriefCollectors(ensureModel);
  }, [open, wsId, agents, ensurePeriodBriefCollectors]);

  const canSubmit =
    Boolean(agentId) && collectorIds.length > 0 && agents.length > 0 && !submitting && !ensuring;

  const submit = async () => {
    if (!agentId) {
      showErrorToast(t(($) => $.notes_page.period_brief_agent_required));
      return;
    }
    if (collectorIds.length === 0) {
      showErrorToast(t(($) => $.notes_page.period_brief_collectors_required));
      return;
    }
    setSubmitting(true);
    try {
      const result = await api.createNotePeriodBrief({
        window: windowKind,
        date,
        timezone,
        agent_id: agentId,
        collector_agent_ids: collectorIds,
      });
      if (!result.job?.id) {
        throw new Error(t(($) => $.notes_page.period_brief_failed));
      }
      toast.success(
        t(($) => $.notes_page.period_brief_created, {
          title: result.page.title || result.window.label || "工作介绍",
          count: result.fact_count ?? 0,
        }),
      );
      onOpenChange(false);
      onCreated?.(result);
      const chatJob = result.collector_jobs?.[0] ?? result.job;
      void openNoteWorkerChat(chatJob);
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.notes_page.period_brief_failed),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[min(90vh,40rem)] w-full flex-col gap-4 overflow-hidden sm:max-w-md">
        <DialogHeader className="min-w-0 shrink-0 pr-8">
          <DialogTitle>{t(($) => $.notes_page.period_brief_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.period_brief_description)}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto overflow-x-hidden py-1">
          <div className="min-w-0 space-y-2">
            <Label>{t(($) => $.notes_page.period_brief_window_label)}</Label>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                size="sm"
                variant={windowKind === "day" ? "default" : "outline"}
                onClick={() => setWindowKind("day")}
                disabled={submitting || ensuring}
              >
                {t(($) => $.notes_page.period_brief_window_day)}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={windowKind === "week" ? "default" : "outline"}
                onClick={() => setWindowKind("week")}
                disabled={submitting || ensuring}
              >
                {t(($) => $.notes_page.period_brief_window_week)}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={windowKind === "month" ? "default" : "outline"}
                onClick={() => setWindowKind("month")}
                disabled={submitting || ensuring}
              >
                {t(($) => $.notes_page.period_brief_window_month)}
              </Button>
            </div>
          </div>
          <div className="min-w-0 space-y-2">
            <Label htmlFor="note-period-brief-date">{t(($) => $.notes_page.period_brief_date_label)}</Label>
            <Input
              id="note-period-brief-date"
              type="date"
              className="w-full max-w-full"
              value={date}
              onChange={(event) => setDate(event.target.value)}
              disabled={submitting || ensuring}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_timezone_hint, { timezone })}
            </p>
          </div>
          <div className="min-w-0 space-y-2">
            <Label>{t(($) => $.notes_page.period_brief_collectors_label)}</Label>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_collectors_hint)}
            </p>
            <div
              className="max-h-40 min-w-0 space-y-1 overflow-x-hidden overflow-y-auto rounded-md border p-1"
              data-testid="period-brief-collectors"
            >
              {collectorAgents.length === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
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
                        "flex w-full min-w-0 max-w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70",
                        selected && "bg-muted text-foreground",
                        !online && "opacity-60",
                      )}
                      onClick={() =>
                        setCollectorOverride((current) =>
                          togglePeriodBriefCollectorId(current ?? defaultCollectors, agent.id),
                        )
                      }
                      disabled={submitting || ensuring}
                      data-testid={`period-brief-collector-${agent.id}`}
                    >
                      <RuntimeIcon className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 overflow-hidden">
                        <span className="block truncate">{name}</span>
                        <span className="block truncate text-xs text-muted-foreground">
                          {isCloud
                            ? t(($) => $.notes_page.period_brief_collector_cloud)
                            : t(($) => $.notes_page.period_brief_collector_local)}
                          {!online
                            ? ` · ${t(($) => $.notes_page.period_brief_collector_offline)}`
                            : ""}
                        </span>
                      </span>
                      {selected ? <Check className="size-4 shrink-0 text-primary" /> : null}
                    </button>
                  );
                })
              )}
            </div>
          </div>
          <div className="min-w-0 space-y-2">
            <Label>{t(($) => $.notes_page.period_brief_agent_label)}</Label>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_agent_hint, {
                name: PERIOD_BRIEF_AGENT_DISPLAY_NAME,
              })}
            </p>
            <div className="max-h-40 min-w-0 space-y-1 overflow-x-hidden overflow-y-auto rounded-md border p-1">
              {agents.length === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                  {ensuring
                    ? t(($) => $.notes_page.period_brief_agent_ensuring)
                    : t(($) => $.notes_page.ai_agent_empty)}
                </div>
              ) : (
                agents.map((agent) => {
                  const selected = agentId === agent.id;
                  const name = resolveActorDisplayName(agent, agent.name || agent.id);
                  const isDefault = isPeriodBriefAgent(agent);
                  return (
                    <button
                      key={agent.id}
                      type="button"
                      className={cn(
                        "flex w-full min-w-0 max-w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70",
                        selected && "bg-muted text-foreground",
                      )}
                      onClick={() => setAgentOverride(agent.id)}
                      disabled={submitting || ensuring}
                      data-testid={isDefault ? "period-brief-default-agent" : undefined}
                    >
                      <Bot className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 overflow-hidden">
                        <span className="block truncate">{name}</span>
                        {isDefault ? (
                          <span className="block truncate text-xs text-muted-foreground">
                            {t(($) => $.notes_page.period_brief_agent_default_badge)}
                          </span>
                        ) : null}
                      </span>
                      {selected ? <Check className="size-4 shrink-0 text-primary" /> : null}
                    </button>
                  );
                })
              )}
            </div>
          </div>
        </div>
        <DialogFooter className="min-w-0 shrink-0">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t(($) => $.notes_page.cancel)}
          </Button>
          <Button type="button" onClick={() => void submit()} disabled={!canSubmit}>
            {submitting || ensuring ? <Loader2 className="size-4 animate-spin" /> : <ClipboardList className="size-4" />}
            {submitting
              ? t(($) => $.notes_page.period_brief_submitting)
              : t(($) => $.notes_page.period_brief_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
