"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@multica/core/api";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import {
  useCommentDraftStore,
  type CommentDraftKey,
} from "@multica/core/issues/stores";
import type { Attachment } from "@multica/core/types";
import { contentReferencesAttachment } from "@multica/core/types";
import { type ContentEditorRef, useFileDropZone } from "../../editor";
import { useCommentTriggerPreview } from "./use-comment-trigger-preview";

type SubmitComment = (
  content: string,
  attachmentIds?: string[],
  suppressAgentIds?: string[],
) => Promise<void>;

/**
 * Owns the shared behavior behind top-level comment and threaded reply
 * composers. Their visual shells intentionally stay separate, while draft
 * persistence, uploads, trigger suppression, and submit cleanup live here.
 */
export function useCommentComposer({
  issueId,
  parentId,
  draftKey,
  onSubmit,
}: {
  issueId: string;
  parentId?: string;
  draftKey?: CommentDraftKey;
  onSubmit: SubmitComment;
}) {
  const editorRef = useRef<ContentEditorRef>(null);
  const [initialDraft] = useState(() =>
    draftKey ? useCommentDraftStore.getState().getDraft(draftKey) : undefined,
  );
  const [content, setContent] = useState(initialDraft ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [suppressedAgentIds, setSuppressedAgentIds] = useState<Set<string>>(
    () => new Set(),
  );
  const [pendingAttachments, setPendingAttachments] = useState<Attachment[]>([]);
  const setDraft = useCommentDraftStore((state) => state.setDraft);
  const clearDraft = useCommentDraftStore((state) => state.clearDraft);
  const { uploadWithToast } = useFileUpload(api);
  const triggerPreview = useCommentTriggerPreview({ issueId, parentId, content });
  const { isDragOver, dropZoneProps } = useFileDropZone({
    onDrop: (files) => files.forEach((file) => editorRef.current?.uploadFile(file)),
  });

  useEffect(() => {
    if (!draftKey) return;
    const flush = () => {
      const markdown = editorRef.current?.getMarkdown();
      if (markdown?.trim()) setDraft(draftKey, markdown);
    };
    const flushWhenHidden = () => {
      if (document.visibilityState === "hidden") flush();
    };
    document.addEventListener("visibilitychange", flushWhenHidden);
    window.addEventListener("pagehide", flush);
    return () => {
      document.removeEventListener("visibilitychange", flushWhenHidden);
      window.removeEventListener("pagehide", flush);
    };
  }, [draftKey, setDraft]);

  useEffect(() => {
    const visibleAgentIds = new Set(
      triggerPreview.agents.map((agent) => agent.id),
    );
    setSuppressedAgentIds((current) => {
      const next = new Set(
        [...current].filter((agentId) => visibleAgentIds.has(agentId)),
      );
      return next.size === current.size ? current : next;
    });
  }, [triggerPreview.agents]);

  const uploadFile = useCallback(
    async (file: File) => {
      const attachment = await uploadWithToast(file, { issueId });
      if (attachment) {
        setPendingAttachments((current) => [...current, attachment]);
      }
      return attachment;
    },
    [issueId, uploadWithToast],
  );

  const updateContent = useCallback(
    (markdown: string) => {
      setContent(markdown);
      if (!draftKey) return;
      if (markdown.trim()) setDraft(draftKey, markdown);
      else clearDraft(draftKey);
    },
    [clearDraft, draftKey, setDraft],
  );

  const toggleSuppressedAgent = useCallback((agentId: string) => {
    setSuppressedAgentIds((current) => {
      const next = new Set(current);
      if (next.has(agentId)) next.delete(agentId);
      else next.add(agentId);
      return next;
    });
  }, []);

  const submit = async () => {
    const markdown = editorRef.current
      ?.getMarkdown()
      ?.replace(/(\n\s*)+$/, "")
      .trim();
    if (!markdown || submitting) return;

    const attachmentIds = pendingAttachments
      .filter((attachment) =>
        contentReferencesAttachment(markdown, attachment),
      )
      .map((attachment) => attachment.id);
    const suppressAgentIds = triggerPreview.agents
      .filter((agent) => suppressedAgentIds.has(agent.id))
      .map((agent) => agent.id);

    setSubmitting(true);
    try {
      await onSubmit(
        markdown,
        attachmentIds.length > 0 ? attachmentIds : undefined,
        suppressAgentIds.length > 0 ? suppressAgentIds : undefined,
      );
      editorRef.current?.clearContent();
      setContent("");
      setSuppressedAgentIds(new Set());
      setPendingAttachments([]);
      if (draftKey) clearDraft(draftKey);
    } finally {
      setSubmitting(false);
    }
  };

  return {
    editor: {
      ref: editorRef,
      defaultValue: initialDraft,
      attachments: pendingAttachments,
      onUpdate: updateContent,
      onSubmit: submit,
      onUploadFile: uploadFile,
    },
    triggers: {
      agents: triggerPreview.agents,
      suppressedAgentIds,
      onToggle: toggleSuppressedAgent,
    },
    dropZone: {
      isDragOver,
      props: dropZoneProps,
    },
    isEmpty: !content.trim(),
    submitting,
  };
}
