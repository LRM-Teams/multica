"use client";

import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { knowledgeKeys } from "@multica/core/knowledge";
import { useWorkspacePaths } from "@multica/core/paths";
import { showErrorToast } from "@multica/ui/lib/error-toast";
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
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

export type PromoteWikiTarget = "context" | "decision";

export interface PromoteToWikiDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  targetKind: PromoteWikiTarget;
  sourceType: "issue" | "channel";
  sourceId: string;
  initialTitle?: string;
  initialContent?: string;
  /** Channel id for CONTEXT, project id for DECISION. */
  subjectId?: string;
}

export function PromoteToWikiDialog({
  open,
  onOpenChange,
  targetKind,
  sourceType,
  sourceId,
  initialTitle = "",
  initialContent = "",
  subjectId = "",
}: PromoteToWikiDialogProps) {
  const { t } = useT("knowledge");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { push } = useNavigation();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(initialTitle);
  const [content, setContent] = useState(initialContent);
  const [subject, setSubject] = useState(subjectId);

  useEffect(() => {
    if (!open) return;
    setTitle(initialTitle);
    setContent(initialContent);
    setSubject(subjectId);
  }, [open, initialTitle, initialContent, subjectId]);

  const mutation = useMutation({
    mutationFn: async () => {
      if (!wsId) throw new Error("workspace missing");
      return api.promoteKnowledgePage(wsId, {
        source_type: sourceType,
        source_id: sourceId,
        target_kind: targetKind,
        title: title.trim(),
        content: content.trim(),
        subject_id: subject.trim() || undefined,
      });
    },
    onSuccess: async (page) => {
      toast.success(t(($) => $.promote.success));
      await queryClient.invalidateQueries({ queryKey: knowledgeKeys.all(wsId ?? "") });
      onOpenChange(false);
      if (page.id) push(paths.wikiDetail(page.id));
    },
    onError: (err) => {
      const message =
        err instanceof ApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : t(($) => $.promote.error);
      showErrorToast(message);
    },
  });

  const canSubmit = title.trim().length > 0 && content.trim().length > 0 && !mutation.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg" data-testid="promote-to-wiki-dialog">
        <DialogHeader>
          <DialogTitle>
            {targetKind === "context"
              ? t(($) => $.promote.dialog_title_context)
              : t(($) => $.promote.dialog_title_decision)}
          </DialogTitle>
          <DialogDescription>{t(($) => $.promote.dialog_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-1">
          <div className="space-y-1.5">
            <Label htmlFor="wiki-promote-title">{t(($) => $.promote.title_label)}</Label>
            <Input
              id="wiki-promote-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="wiki-promote-content">{t(($) => $.promote.content_label)}</Label>
            <Textarea
              id="wiki-promote-content"
              value={content}
              onChange={(e) => setContent(e.target.value)}
              rows={8}
              className="min-h-40 resize-y"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="wiki-promote-subject">
              {targetKind === "context"
                ? t(($) => $.promote.subject_label_context)
                : t(($) => $.promote.subject_label_decision)}
            </Label>
            <Input
              id="wiki-promote-subject"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder={t(($) => $.promote.subject_placeholder)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.promote.cancel)}
          </Button>
          <Button
            type="button"
            disabled={!canSubmit}
            onClick={() => mutation.mutate()}
          >
            {mutation.isPending ? t(($) => $.promote.submitting) : t(($) => $.promote.submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
