"use client";

import { ArrowUp, Loader2 } from "lucide-react";
import { ContentEditor, FileDropOverlay } from "../../editor";
import { FileUploadButton } from "@multica/ui/components/common/file-upload-button";
import { ActorAvatar } from "../../common/actor-avatar";
import type { CommentDraftKey } from "@multica/core/issues/stores";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { useCommentComposer } from "../hooks/use-comment-composer";
import { CommentTriggerChips } from "./comment-trigger-chips";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ReplyInputProps {
  issueId: string;
  parentId: string;
  placeholder?: string;
  avatarType: string;
  avatarId: string;
  onSubmit: (content: string, attachmentIds?: string[], suppressAgentIds?: string[]) => Promise<void>;
  size?: "sm" | "default";
  /** When set, hydrates/persists the in-progress reply via the draft store.
   *  Required for replies inside virtualized timeline threads, where the
   *  enclosing CommentCard may unmount on scroll-out. */
  draftKey?: CommentDraftKey;
}

// ---------------------------------------------------------------------------
// ReplyInput
// ---------------------------------------------------------------------------

function ReplyInput({
  issueId,
  parentId,
  placeholder,
  avatarType,
  avatarId,
  onSubmit,
  size = "default",
  draftKey,
}: ReplyInputProps) {
  const { t } = useT("issues");
  const placeholderText = placeholder ?? t(($) => $.reply.placeholder);
  const composer = useCommentComposer({
    issueId,
    parentId,
    draftKey,
    onSubmit,
  });

  const avatarSize = size === "sm" ? 22 : 28;

  return (
    <div className="group/editor flex items-start gap-2.5">
      <ActorAvatar
        actorType={avatarType}
        actorId={avatarId}
        size={avatarSize}
        className="mt-0.5 shrink-0"
      />
      <div
        {...composer.dropZone.props}
        className={cn(
          "relative min-w-0 flex-1 flex flex-col",
          !composer.isEmpty && "pb-9",
        )}
      >
        <div className="flex-1 min-h-0 overflow-y-auto">
          <ContentEditor
            ref={composer.editor.ref}
            defaultValue={composer.editor.defaultValue}
            placeholder={placeholderText}
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
        <div className="absolute bottom-0 left-0 right-24 min-w-0">
          <CommentTriggerChips
            agents={composer.triggers.agents}
            suppressedAgentIds={composer.triggers.suppressedAgentIds}
            onToggle={composer.triggers.onToggle}
          />
        </div>
        <div className="absolute bottom-0 right-0 flex items-center gap-1">
          <FileUploadButton
            size="sm"
            multiple
            onSelect={(file) => composer.editor.ref.current?.uploadFile(file)}
          />
          <button
            type="button"
            disabled={composer.isEmpty || composer.submitting}
            onClick={composer.editor.onSubmit}
            className={cn(
              "inline-flex h-6 w-6 items-center justify-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-50",
              composer.isEmpty
                ? "text-muted-foreground hover:bg-accent hover:text-foreground"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {composer.submitting ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <ArrowUp className="h-3.5 w-3.5" />
            )}
          </button>
        </div>
        {composer.dropZone.isDragOver && <FileDropOverlay />}
      </div>
    </div>
  );
}

export { ReplyInput, type ReplyInputProps };
