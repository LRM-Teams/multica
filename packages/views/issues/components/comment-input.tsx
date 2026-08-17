"use client";

import { ContentEditor, FileDropOverlay } from "../../editor";
import { FileUploadButton } from "@multica/ui/components/common/file-upload-button";
import { SubmitButton } from "@multica/ui/components/common/submit-button";
import { enterKey, formatShortcut, modKey } from "@multica/core/platform";
import { useT } from "../../i18n";
import { useCommentComposer } from "../hooks/use-comment-composer";
import { CommentTriggerChips } from "./comment-trigger-chips";

interface CommentInputProps {
  issueId: string;
  onSubmit: (content: string, attachmentIds?: string[], suppressAgentIds?: string[]) => Promise<void>;
}

function CommentInput({ issueId, onSubmit }: CommentInputProps) {
  const { t } = useT("issues");
  const draftKey = `new:${issueId}` as const;
  const composer = useCommentComposer({
    issueId,
    draftKey,
    onSubmit,
  });

  return (
    <div
      {...composer.dropZone.props}
      data-testid="issue-comment-composer"
      className="relative flex flex-col rounded-lg bg-card pb-8 ring-1 ring-border"
    >
      <div className="flex-1 min-h-0 overflow-y-auto px-3 py-2">
        <ContentEditor
          ref={composer.editor.ref}
          defaultValue={composer.editor.defaultValue}
          placeholder={t(($) => $.comment.leave_comment_placeholder)}
          onUpdate={composer.editor.onUpdate}
          onSubmit={composer.editor.onSubmit}
          onUploadFile={composer.editor.onUploadFile}
          debounceMs={100}
          currentIssueId={issueId}
          attachments={composer.editor.attachments}
          enableSlashCommands
          slashCommandMode="command"
        />
      </div>
      <div className="absolute bottom-1 left-2 right-28 min-w-0">
        <CommentTriggerChips
          agents={composer.triggers.agents}
          suppressedAgentIds={composer.triggers.suppressedAgentIds}
          onToggle={composer.triggers.onToggle}
        />
      </div>
      <div className="absolute bottom-1 right-1.5 flex items-center gap-1">
        <FileUploadButton
          size="sm"
          multiple
          onSelect={(file) => composer.editor.ref.current?.uploadFile(file)}
        />
        <SubmitButton
          onClick={composer.editor.onSubmit}
          disabled={composer.isEmpty}
          loading={composer.submitting}
          tooltip={`${t(($) => $.comment.send_tooltip)} · ${formatShortcut(modKey, enterKey)}`}
        />
      </div>
      {composer.dropZone.isDragOver && <FileDropOverlay />}
    </div>
  );
}

export { CommentInput };
