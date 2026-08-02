"use client";

import { useRef } from "react";
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

/** Remount form when the promote target opens / source changes. */
export function PromoteToWikiDialog(props: PromoteToWikiDialogProps) {
  const resetKey = props.open
    ? `${props.sourceType}:${props.sourceId}:${props.targetKind}:${props.subjectId ?? ""}:${props.initialTitle ?? ""}`
    : "closed";
  return <PromoteToWikiDialogForm key={resetKey} {...props} />;
}

function PromoteToWikiDialogForm({
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
  const titleRef = useRef<HTMLInputElement>(null);
  const contentRef = useRef<HTMLTextAreaElement>(null);
  const subjectRef = useRef<HTMLInputElement>(null);

  const mutation = useMutation({
    mutationFn: async () => {
      if (!wsId) throw new Error("workspace missing");
      const title = titleRef.current?.value.trim() ?? "";
      const content = contentRef.current?.value.trim() ?? "";
      const subject = subjectRef.current?.value.trim() ?? "";
      if (!title || !content) throw new Error(t(($) => $.promote.error));
      return api.promoteKnowledgePage(wsId, {
        source_type: sourceType,
        source_id: sourceId,
        target_kind: targetKind,
        title,
        content,
        subject_id: subject || undefined,
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
              ref={titleRef}
              defaultValue={initialTitle}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="wiki-promote-content">{t(($) => $.promote.content_label)}</Label>
            <Textarea
              id="wiki-promote-content"
              ref={contentRef}
              defaultValue={initialContent}
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
              ref={subjectRef}
              defaultValue={subjectId}
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
            disabled={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {mutation.isPending ? t(($) => $.promote.submitting) : t(($) => $.promote.submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
