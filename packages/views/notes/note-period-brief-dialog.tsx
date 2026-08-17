/**
 * @vitest-environment happy-dom
 */
"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Check, ClipboardList, Loader2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  PERIOD_BRIEF_AGENT_DISPLAY_NAME,
  isPeriodBriefAgent,
  resolvePeriodBriefSynthesizerId,
} from "@multica/core/notes/period-brief-agent";
import { computerListOptions, localMachineWorkUncollected, runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type {
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
  const userId = useAuthStore((s) => s.user?.id);
  const queryClient = useQueryClient();
  const { openNoteWorkerChat } = useOpenNoteWorkerChat();
  const { data: computers, isSuccess: computersLoaded } = useQuery({
    ...computerListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId),
    enabled: Boolean(wsId) && open,
  });
  const machineWorkUncollected =
    computersLoaded && localMachineWorkUncollected(computers, userId);
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
  const [agentId, setAgentId] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [ensuring, setEnsuring] = useState(false);
  const ensureAttemptedRef = useRef(false);

  const resolvedPreferredAgentId = resolvePeriodBriefSynthesizerId(agents, preferredAgentId);

  // Reset agent pick when the dialog opens — adjust during render (prev ref), not an effect.
  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      setAgentId(resolvedPreferredAgentId);
      setWindowKind("week");
      setDate(today);
      setSubmitting(false);
      setEnsuring(false);
      ensureAttemptedRef.current = false;
    }
  } else if (open && resolvedPreferredAgentId && !agentId) {
    setAgentId(resolvedPreferredAgentId);
  }

  useEffect(() => {
    if (!open || !wsId || ensureAttemptedRef.current) return;
    if (agents.some((agent) => isPeriodBriefAgent(agent))) return;
    const donor = agents.find((agent) => agent.runtime_id && agent.model?.trim());
    const runtimeId = donor?.runtime_id ?? runtimes[0]?.id;
    const model = donor?.model?.trim();
    if (!runtimeId || !model) return;

    ensureAttemptedRef.current = true;
    let cancelled = false;
    setEnsuring(true);
    void api
      .ensurePeriodBriefAgent(runtimeId, model)
      .then((result) => {
        if (cancelled) return;
        queryClient.setQueryData(workspaceKeys.agents(wsId), (current: typeof agents = []) => {
          if (current.some((agent) => agent.id === result.agent.id)) return current;
          return [...current, result.agent];
        });
        void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
        setAgentId((current) => {
          if (!current) return result.agent.id;
          // Upgrade to 周报 only while still on a non-specialist pick.
          if (!agents.some((agent) => isPeriodBriefAgent(agent) && agent.id === current)) {
            return result.agent.id;
          }
          return current;
        });
      })
      .catch(() => {
        // Non-fatal: user can still pick any existing Agent.
      })
      .finally(() => {
        if (!cancelled) setEnsuring(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, wsId, agents, runtimes, queryClient]);

  const canSubmit = Boolean(agentId) && agents.length > 0 && !submitting && !ensuring;

  const submit = async () => {
    if (!agentId) {
      showErrorToast(t(($) => $.notes_page.period_brief_agent_required));
      return;
    }
    setSubmitting(true);
    try {
      const result = await api.createNotePeriodBrief({
        window: windowKind,
        date,
        timezone,
        agent_id: agentId,
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
      void openNoteWorkerChat(result.job);
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.period_brief_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.period_brief_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label>{t(($) => $.notes_page.period_brief_window_label)}</Label>
            <div className="flex gap-2">
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
          <div className="space-y-2">
            <Label htmlFor="note-period-brief-date">{t(($) => $.notes_page.period_brief_date_label)}</Label>
            <Input
              id="note-period-brief-date"
              type="date"
              value={date}
              onChange={(event) => setDate(event.target.value)}
              disabled={submitting || ensuring}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_timezone_hint, { timezone })}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t(($) => $.notes_page.period_brief_agent_label)}</Label>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_agent_hint, {
                name: PERIOD_BRIEF_AGENT_DISPLAY_NAME,
              })}
            </p>
            <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border p-1">
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
                        "flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70",
                        selected && "bg-muted text-foreground",
                      )}
                      onClick={() => setAgentId(agent.id)}
                      disabled={submitting || ensuring}
                      data-testid={isDefault ? "period-brief-default-agent" : undefined}
                    >
                      <Bot className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate">
                        {name}
                        {isDefault ? (
                          <span className="ml-2 text-xs text-muted-foreground">
                            {t(($) => $.notes_page.period_brief_agent_default_badge)}
                          </span>
                        ) : null}
                      </span>
                      {selected ? <Check className="size-4 text-primary" /> : null}
                    </button>
                  );
                })
              )}
            </div>
          </div>
          {machineWorkUncollected ? (
            <p className="text-xs text-muted-foreground" data-testid="local-machine-work-uncollected">
              {t(($) => $.notes_page.period_brief_local_work_uncollected)}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t(($) => $.notes_page.cancel)}
          </Button>
          <Button type="button" onClick={() => void submit()} disabled={!canSubmit}>
            {submitting || ensuring ? <Loader2 className="size-4 animate-spin" /> : <ClipboardList className="size-4" />}
            {t(($) => $.notes_page.period_brief_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
