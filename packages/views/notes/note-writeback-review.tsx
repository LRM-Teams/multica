"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Loader2, X } from "lucide-react";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { noteKeys, noteWritebacksOptions } from "@multica/core/notes/queries";
import { previewNoteWritebackContent, writebackHasOpenableEvidence } from "@multica/core/notes/writeback-preview";
import { useWorkspacePaths } from "@multica/core/paths";
import type { NotePage, NoteWriteback, NoteWritebackEvidence } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { NoteAIDiffPreview } from "../editor/note-ai-diff";
import { useT } from "../i18n/use-t";
import { AppLink } from "../navigation";

function evidenceHref(
  item: NoteWritebackEvidence,
  evidence: NoteWritebackEvidence[],
  paths: ReturnType<typeof useWorkspacePaths>,
): string | null {
  const type = item.type.trim().toLowerCase();
  const id = item.id.trim();
  if (!id) return null;
  if (type === "issue") return paths.issueDetail(id);
  if (type === "run" || type === "task" || type === "agent_task") {
    const issueId = evidence.find((entry) => entry.type.trim().toLowerCase() === "issue" && entry.id.trim())?.id.trim();
    // Runs are viewed from the related issue until a dedicated run route exists.
    return issueId ? paths.issueDetail(issueId) : paths.issues();
  }
  return null;
}

function EvidenceLinks({
  evidence,
  paths,
}: {
  evidence: NoteWritebackEvidence[];
  paths: ReturnType<typeof useWorkspacePaths>;
}) {
  const { t } = useT("layout");
  return (
    <div className="flex flex-wrap gap-1.5" data-testid="note-writeback-evidence">
      {evidence.map((item) => {
        const href = evidenceHref(item, evidence, paths);
        const label = item.label?.trim() || item.id;
        const typeLabel = item.type.trim() || "ref";
        const text = `${typeLabel}: ${label}`;
        if (!href) {
          return (
            <span
              key={`${item.type}:${item.id}`}
              className="inline-flex items-center rounded-md border bg-background px-2 py-0.5 text-[11px] text-muted-foreground"
            >
              {text}
            </span>
          );
        }
        return (
          <AppLink
            key={`${item.type}:${item.id}`}
            href={href}
            className="inline-flex items-center rounded-md border bg-background px-2 py-0.5 text-[11px] text-foreground underline-offset-2 hover:underline"
            title={t(($) => $.notes_page.writeback_open_evidence)}
          >
            {text}
          </AppLink>
        );
      })}
    </div>
  );
}

function WritebackCard({
  writeback,
  currentContent,
  busyId,
  onAccept,
  onReject,
}: {
  writeback: NoteWriteback;
  currentContent: string;
  busyId: string | null;
  onAccept: (writeback: NoteWriteback) => void;
  onReject: (writeback: NoteWriteback) => void;
}) {
  const { t } = useT("layout");
  const paths = useWorkspacePaths();
  const preview = previewNoteWritebackContent(currentContent, writeback);
  const before =
    writeback.action === "patch" && writeback.target
      ? writeback.target
      : writeback.action === "append"
        ? currentContent
        : currentContent;
  const after =
    writeback.action === "patch"
      ? writeback.content
      : preview ?? writeback.content;
  const actionLabel =
    writeback.action === "append"
      ? t(($) => $.notes_page.writeback_action_append)
      : writeback.action === "patch"
        ? t(($) => $.notes_page.writeback_action_patch)
        : writeback.action === "replace_page"
          ? t(($) => $.notes_page.writeback_action_replace_page)
          : writeback.action;
  const busy = busyId === writeback.id;
  const canAccept = writebackHasOpenableEvidence(writeback.evidence) && preview !== null;

  return (
    <div
      className="overflow-hidden rounded-xl border bg-card text-card-foreground shadow-sm"
      data-testid="note-writeback-card"
      data-writeback-id={writeback.id}
    >
      <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3.5 py-2.5">
        <div className="min-w-0">
          <div className="text-xs font-medium text-foreground">{actionLabel}</div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">
            {t(($) => $.notes_page.writeback_pending_hint)}
          </div>
        </div>
        <div className="flex shrink-0 gap-1.5">
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => onReject(writeback)}
            data-testid="note-writeback-reject"
          >
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : <X className="size-3.5" />}
            {t(($) => $.notes_page.writeback_reject)}
          </Button>
          <Button
            size="sm"
            disabled={busy || !canAccept}
            onClick={() => onAccept(writeback)}
            data-testid="note-writeback-accept"
            title={!canAccept ? t(($) => $.notes_page.writeback_accept_blocked) : undefined}
          >
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Check className="size-3.5" />}
            {t(($) => $.notes_page.writeback_accept)}
          </Button>
        </div>
      </div>
      <div className="space-y-3 p-3.5">
        <div>
          <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            {t(($) => $.notes_page.writeback_evidence)}
          </div>
          <EvidenceLinks evidence={writeback.evidence ?? []} paths={paths} />
        </div>
        {writeback.action === "append" ? (
          <div className="max-h-48 overflow-y-auto whitespace-pre-wrap rounded-lg border bg-muted/30 px-3 py-2 text-sm leading-6">
            {writeback.content}
          </div>
        ) : (
          <NoteAIDiffPreview
            before={before}
            after={after}
            beforeLabel={t(($) => $.notes_page.writeback_diff_before)}
            afterLabel={t(($) => $.notes_page.writeback_diff_after)}
            emptyLabel={t(($) => $.notes_page.writeback_diff_empty)}
            omittedLabel={t(($) => $.notes_page.writeback_diff_omitted)}
          />
        )}
      </div>
    </div>
  );
}

export function NoteWritebackReview({
  page,
  currentContent,
  onAppliedContent,
}: {
  page: NotePage;
  currentContent: string;
  onAppliedContent: (content: string) => void;
}) {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const { data } = useQuery(noteWritebacksOptions(wsId, page.id, "pending"));
  const writebacks = data?.writebacks ?? [];
  const [busyId, setBusyId] = useState<string | null>(null);

  if (writebacks.length === 0) return null;

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: noteKeys.writebacks(wsId, page.id, "pending") }),
      queryClient.invalidateQueries({ queryKey: noteKeys.detail(wsId, page.id) }),
      queryClient.invalidateQueries({ queryKey: noteKeys.list(wsId) }),
    ]);
  };

  const accept = async (writeback: NoteWriteback) => {
    if (busyId) return;
    if (!writebackHasOpenableEvidence(writeback.evidence)) {
      showErrorToast(t(($) => $.notes_page.writeback_accept_blocked));
      return;
    }
    const next = previewNoteWritebackContent(currentContent, writeback);
    if (next === null) {
      showErrorToast(t(($) => $.notes_page.writeback_preview_failed));
      return;
    }
    setBusyId(writeback.id);
    try {
      await api.acceptNotePageWriteback(writeback.id);
      onAppliedContent(next);
      queryClient.setQueryData<NotePage>(noteKeys.detail(wsId, page.id), (old) =>
        old ? { ...old, content: next } : old,
      );
      toast.success(t(($) => $.notes_page.writeback_accepted));
      await invalidate();
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.notes_page.writeback_accept_failed),
      );
    } finally {
      setBusyId(null);
    }
  };

  const reject = async (writeback: NoteWriteback) => {
    if (busyId) return;
    setBusyId(writeback.id);
    try {
      await api.rejectNotePageWriteback(writeback.id);
      toast.success(t(($) => $.notes_page.writeback_rejected));
      await invalidate();
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.notes_page.writeback_reject_failed),
      );
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="mb-4 space-y-3" data-testid="note-writeback-review">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {t(($) => $.notes_page.writeback_review_title, { count: writebacks.length })}
        </div>
      </div>
      {writebacks.map((writeback) => (
        <WritebackCard
          key={writeback.id}
          writeback={writeback}
          currentContent={currentContent}
          busyId={busyId}
          onAccept={(item) => void accept(item)}
          onReject={(item) => void reject(item)}
        />
      ))}
    </div>
  );
}
