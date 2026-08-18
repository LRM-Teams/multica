/**
 * @vitest-environment happy-dom
 */
"use client";

import { useMemo, useState } from "react";
import { CalendarDays, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { computerListOptions, localMachineWorkUncollected } from "@multica/core/runtimes";
import type {
  CreateNoteRetrospectiveResponse,
  NoteRetrospectiveSource,
  NoteRetrospectiveWindow,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
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
import { useT } from "../i18n/use-t";
import { useViewingTimezone } from "../common/use-viewing-timezone";
import { showErrorToast } from "@multica/ui/lib/error-toast";

const SOURCE_OPTIONS: NoteRetrospectiveSource[] = ["issue_activity", "touched_notes", "agent_runs"];

export function NoteRetrospectiveDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (result: CreateNoteRetrospectiveResponse) => void;
}) {
  const { t } = useT("layout");
  const timezone = useViewingTimezone();
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);
  const { data: computers, isSuccess: computersLoaded } = useQuery({
    ...computerListOptions(wsId),
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
  const [windowKind, setWindowKind] = useState<NoteRetrospectiveWindow>("day");
  const [date, setDate] = useState(today);
  const [sources, setSources] = useState<NoteRetrospectiveSource[]>(["issue_activity", "touched_notes"]);
  const [submitting, setSubmitting] = useState(false);

  const toggleSource = (source: NoteRetrospectiveSource, checked: boolean) => {
    setSources((current) => {
      if (checked) return current.includes(source) ? current : [...current, source];
      return current.filter((item) => item !== source);
    });
  };

  const submit = async () => {
    if (sources.length === 0) {
      showErrorToast(t(($) => $.notes_page.retrospective_sources_required));
      return;
    }
    setSubmitting(true);
    try {
      const result = await api.createNoteRetrospective({
        window: windowKind,
        date,
        timezone,
        sources,
      });
      if (!result.page?.id) {
        throw new Error(t(($) => $.notes_page.retrospective_failed));
      }
      toast.success(
        t(($) => $.notes_page.retrospective_created, {
          title: result.page.title || result.window.label || "回顾",
          count: result.fact_count ?? 0,
        }),
      );
      onOpenChange(false);
      onCreated(result);
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.notes_page.retrospective_failed),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.retrospective_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.retrospective_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label>{t(($) => $.notes_page.retrospective_window_label)}</Label>
            <div className="flex gap-2">
              <Button
                type="button"
                size="sm"
                variant={windowKind === "day" ? "default" : "outline"}
                onClick={() => setWindowKind("day")}
              >
                {t(($) => $.notes_page.retrospective_window_day)}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={windowKind === "week" ? "default" : "outline"}
                onClick={() => setWindowKind("week")}
              >
                {t(($) => $.notes_page.retrospective_window_week)}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={windowKind === "month" ? "default" : "outline"}
                onClick={() => setWindowKind("month")}
              >
                {t(($) => $.notes_page.retrospective_window_month)}
              </Button>
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="note-retro-date">{t(($) => $.notes_page.retrospective_date_label)}</Label>
            <Input
              id="note-retro-date"
              type="date"
              value={date}
              onChange={(event) => setDate(event.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.notes_page.retrospective_timezone_hint, { timezone })}
            </p>
          </div>
          <div className="space-y-2">
            <Label>{t(($) => $.notes_page.retrospective_sources_label)}</Label>
            <div className="space-y-2">
              {SOURCE_OPTIONS.map((source) => {
                const label =
                  source === "issue_activity"
                    ? t(($) => $.notes_page.retrospective_source_issue_activity)
                    : source === "touched_notes"
                      ? t(($) => $.notes_page.retrospective_source_touched_notes)
                      : t(($) => $.notes_page.retrospective_source_agent_runs);
                const hint =
                  source === "issue_activity"
                    ? t(($) => $.notes_page.retrospective_source_issue_activity_hint)
                    : source === "touched_notes"
                      ? t(($) => $.notes_page.retrospective_source_touched_notes_hint)
                      : t(($) => $.notes_page.retrospective_source_agent_runs_hint);
                return (
                  <label key={source} className="flex items-start gap-2 text-sm">
                    <Checkbox
                      checked={sources.includes(source)}
                      onCheckedChange={(checked) => toggleSource(source, checked === true)}
                    />
                    <span className="min-w-0">
                      <span className="block font-medium">{label}</span>
                      <span className="block text-xs text-muted-foreground">{hint}</span>
                    </span>
                  </label>
                );
              })}
            </div>
          </div>
          {machineWorkUncollected ? (
            <p className="text-xs text-muted-foreground" data-testid="local-machine-work-uncollected">
              {t(($) => $.notes_page.retrospective_local_work_uncollected)}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t(($) => $.notes_page.cancel)}
          </Button>
          <Button type="button" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <CalendarDays className="size-4" />}
            {t(($) => $.notes_page.retrospective_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
