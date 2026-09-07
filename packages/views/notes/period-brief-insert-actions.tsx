"use client";

import { useMemo, useState } from "react";
import { ChevronDown, FilePlus2, ListPlus, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { chatKeys } from "@multica/core/chat/queries";
import {
  abortInFlightNotePageUpdate,
  applyFetchedNotePageToCache,
} from "@multica/core/notes/mutations";
import { noteKeys, noteListOptions } from "@multica/core/notes/queries";
import type { PeriodBriefInsertPropose } from "@multica/core/notes/period-brief-insert";
import { useWorkspaceId } from "@multica/core/hooks";
import type { MessagePart, NotePage, NotePeriodBriefInsertMode } from "@multica/core/types";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { useT } from "../i18n/use-t";

function pageTitle(page: Pick<NotePage, "title"> | undefined, fallback: string): string {
  const title = page?.title.trim();
  return title || fallback;
}

function usePeriodBriefInsertSubmit() {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<NotePeriodBriefInsertMode | null>(null);

  const submit = async (
    runId: string,
    mode: NotePeriodBriefInsertMode,
    targetPageId?: string,
    sourcePageId?: string,
  ) => {
    if (busy !== null) return;
    setBusy(mode);
    try {
      if (targetPageId) abortInFlightNotePageUpdate(targetPageId);
      const res = await api.insertNotePeriodBrief(runId, {
        mode,
        ...(targetPageId ? { target_page_id: targetPageId } : {}),
      });
      const pageId = res.page_id?.trim() || targetPageId || "";
      if (pageId && pageId !== targetPageId) abortInFlightNotePageUpdate(pageId);
      if (wsId) {
        if (mode === "append" && pageId) {
          try {
            const page = await api.getNotePage(pageId);
            applyFetchedNotePageToCache(queryClient, wsId, page);
          } catch {
            void queryClient.invalidateQueries({ queryKey: noteKeys.detail(wsId, pageId) });
          }
        }
        void queryClient.invalidateQueries({ queryKey: noteKeys.list(wsId) });
        void queryClient.invalidateQueries({ queryKey: chatKeys.all(wsId) });
      }
      const named =
        Boolean(res.page_title) &&
        Boolean(targetPageId) &&
        Boolean(sourcePageId) &&
        targetPageId !== sourcePageId;
      toast.success(
        mode === "append"
          ? named
            ? t(($) => $.message.period_brief_insert_below_success_named, {
                title: res.page_title ?? "",
              })
            : t(($) => $.message.period_brief_insert_below_success)
          : named
            ? t(($) => $.message.period_brief_insert_child_success_named, {
                title: res.page_title ?? "",
                child: res.title ?? "",
              })
            : t(($) => $.message.period_brief_insert_child_success, { title: res.title ?? "" }),
      );
    } catch (err) {
      showErrorToast(
        err instanceof Error ? err.message : t(($) => $.message.period_brief_insert_failed),
      );
    } finally {
      setBusy(null);
    }
  };

  return { busy, submit };
}

export function PeriodBriefInsertActions({
  part,
}: {
  part: Extract<MessagePart, { type: "period_brief_insert" }>;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const { data: list } = useQuery({
    ...noteListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const pages = list?.pages ?? [];
  const sourcePageId = part.source_page_id?.trim() ?? "";
  const [targetPageId, setTargetPageId] = useState(sourcePageId);
  const [query, setQuery] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const { busy, submit } = usePeriodBriefInsertSubmit();

  const selected = pages.find((page) => page.id === targetPageId);
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return pages;
    return pages.filter((page) => page.title.toLowerCase().includes(needle));
  }, [pages, query]);
  const targetLabel = selected
    ? pageTitle(selected, t(($) => $.message.period_brief_insert_target_current))
    : sourcePageId && targetPageId === sourcePageId
      ? part.label?.trim() || t(($) => $.message.period_brief_insert_target_current)
      : t(($) => $.message.period_brief_insert_target_current);

  const run = (mode: NotePeriodBriefInsertMode) => {
    void submit(part.ref_id, mode, targetPageId || undefined, sourcePageId || undefined);
  };

  return (
    <div className="mt-2 flex min-w-0 flex-col gap-2" data-testid="period-brief-insert-actions">
      <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
        <span className="shrink-0">{t(($) => $.message.period_brief_insert_target)}</span>
        <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
          <PopoverTrigger
            render={
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="min-w-0 max-w-full"
                data-testid="period-brief-insert-target"
                disabled={busy !== null}
              />
            }
          >
            <span className="truncate">{targetLabel}</span>
            <ChevronDown className="size-3.5 shrink-0" aria-hidden />
          </PopoverTrigger>
          <PopoverContent align="start" className="w-72 p-2" data-testid="period-brief-insert-picker">
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t(($) => $.message.period_brief_insert_search)}
              data-testid="period-brief-insert-search"
              className="h-8"
            />
            <div className="mt-2 max-h-48 overflow-y-auto">
              {filtered.map((page) => (
                <button
                  key={page.id}
                  type="button"
                  data-testid={`period-brief-insert-page-${page.id}`}
                  className={cn(
                    "flex w-full truncate rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent",
                    page.id === targetPageId && "bg-accent",
                  )}
                  onClick={() => {
                    setTargetPageId(page.id);
                    setPickerOpen(false);
                    setQuery("");
                  }}
                >
                  {pageTitle(page, t(($) => $.message.note_brief_untitled))}
                </button>
              ))}
            </div>
          </PopoverContent>
        </Popover>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy !== null}
          data-testid="period-brief-insert-below"
          onClick={(event) => {
            event.stopPropagation();
            run("append");
          }}
        >
          {busy === "append" ? (
            <Loader2 className="size-3.5 animate-spin" aria-hidden />
          ) : (
            <ListPlus className="size-3.5" aria-hidden />
          )}
          {t(($) => $.message.period_brief_insert_below)}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy !== null}
          data-testid="period-brief-insert-child"
          onClick={(event) => {
            event.stopPropagation();
            run("child");
          }}
        >
          {busy === "child" ? (
            <Loader2 className="size-3.5 animate-spin" aria-hidden />
          ) : (
            <FilePlus2 className="size-3.5" aria-hidden />
          )}
          {t(($) => $.message.period_brief_insert_child)}
        </Button>
      </div>
    </div>
  );
}

export function PeriodBriefInsertProposeCard({
  propose,
  sourcePageId,
}: {
  propose: PeriodBriefInsertPropose;
  sourcePageId?: string;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const { data: list } = useQuery({
    ...noteListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const [dismissed, setDismissed] = useState(false);
  const { busy, submit } = usePeriodBriefInsertSubmit();
  const target = list?.pages.find((page) => page.id === propose.targetPageId);
  const title =
    propose.label?.trim() ||
    pageTitle(target, t(($) => $.message.period_brief_insert_target_current));
  if (dismissed) return null;

  return (
    <div
      className="mt-2 flex min-w-0 flex-col gap-2 rounded-md border border-border/70 p-2"
      data-testid="period-brief-insert-propose"
    >
      <p className="text-sm">
        {propose.mode === "append"
          ? t(($) => $.message.period_brief_insert_propose_append, { title })
          : t(($) => $.message.period_brief_insert_propose_child, { title })}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          size="sm"
          disabled={busy !== null}
          data-testid="period-brief-insert-propose-confirm"
          onClick={(event) => {
            event.stopPropagation();
            void submit(propose.runId, propose.mode, propose.targetPageId, sourcePageId);
          }}
        >
          {busy ? <Loader2 className="size-3.5 animate-spin" aria-hidden /> : null}
          {t(($) => $.message.period_brief_insert_confirm)}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={busy !== null}
          data-testid="period-brief-insert-propose-dismiss"
          onClick={(event) => {
            event.stopPropagation();
            setDismissed(true);
          }}
        >
          {t(($) => $.message.period_brief_insert_dismiss)}
        </Button>
      </div>
    </div>
  );
}
