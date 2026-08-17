/**
 * @vitest-environment happy-dom
 */
"use client";

import { useMemo, useRef, useState } from "react";
import { Bot, Check, ClipboardList, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useWorkspaceId } from "@multica/core/hooks";
import { computerListOptions, localMachineWorkUncollected } from "@multica/core/runtimes";
import { agentListOptions } from "@multica/core/workspace/queries";
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
  const { openNoteWorkerChat } = useOpenNoteWorkerChat();
  const { data: computers, isSuccess: computersLoaded } = useQuery({
    ...computerListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: Boolean(wsId),
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

  const resolvedPreferredAgentId =
    preferredAgentId && agents.some((agent) => agent.id === preferredAgentId)
      ? preferredAgentId
      : agents[0]?.id ?? null;

  // Reset agent pick when the dialog opens — adjust during render (prev ref), not an effect.
  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      setAgentId(resolvedPreferredAgentId);
      setWindowKind("week");
      setDate(today);
      setSubmitting(false);
    }
  } else if (open && resolvedPreferredAgentId && !agentId) {
    setAgentId(resolvedPreferredAgentId);
  }

  const canSubmit = Boolean(agentId) && agents.length > 0 && !submitting;

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
                disabled={submitting}
              >
                {t(($) => $.notes_page.period_brief_window_day)}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={windowKind === "week" ? "default" : "outline"}
                onClick={() => setWindowKind("week")}
                disabled={submitting}
              >
                {t(($) => $.notes_page.period_brief_window_week)}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={windowKind === "month" ? "default" : "outline"}
                onClick={() => setWindowKind("month")}
                disabled={submitting}
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
              disabled={submitting}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_timezone_hint, { timezone })}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t(($) => $.notes_page.period_brief_agent_label)}</Label>
            <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border p-1">
              {agents.length === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                  {t(($) => $.notes_page.ai_agent_empty)}
                </div>
              ) : (
                agents.map((agent) => {
                  const selected = agentId === agent.id;
                  const name = resolveActorDisplayName(agent, agent.name || agent.id);
                  return (
                    <button
                      key={agent.id}
                      type="button"
                      className={cn(
                        "flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70",
                        selected && "bg-muted text-foreground",
                      )}
                      onClick={() => setAgentId(agent.id)}
                      disabled={submitting}
                    >
                      <Bot className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate">{name}</span>
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
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <ClipboardList className="size-4" />}
            {t(($) => $.notes_page.period_brief_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
