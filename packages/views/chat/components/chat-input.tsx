"use client";

import type { ReactNode } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import {
  ContentEditor,
  type ContentEditorRef,
  useFileDropZone,
  FileDropOverlay,
} from "../../editor";
import { FileUploadButton } from "@multica/ui/components/common/file-upload-button";
import { SubmitButton } from "@multica/ui/components/common/submit-button";
import { useChatStore, DRAFT_NEW_SESSION } from "@multica/core/chat";
import { useSetChatSessionProject } from "@multica/core/chat/mutations";
import { ProjectPickerButton } from "../../common/project-picker-button";
import { createLogger } from "@multica/core/logger";
import { enterKey } from "@multica/core/platform";
import type { UploadResult } from "@multica/core/hooks/use-file-upload";
import type { MentionItem } from "../../editor/extensions/mention-suggestion";
import { useT } from "../../i18n";

const logger = createLogger("chat.ui");

interface ChatInputProps {
  onSend: (content: string, attachmentIds?: string[]) => void | boolean | Promise<void | boolean>;
  restoreDraftRequest?: { id: string; content: string } | null;
  onRestoreDraftConsumed?: () => void;
  /** Receives a File and returns the attachment row (with id + CDN link).
   *  The wrapper owner (ChatWindow) lazy-creates a chat_session if needed
   *  and forwards `chatSessionId` to the upload — chat-input only cares
   *  about the upload result so it can map URL → id for back-fill on send.
   *  When unset, paste/drag/button still type into the editor but no upload
   *  fires (the editor's file-upload extension is a no-op without a handler). */
  onUploadFile?: (file: File) => Promise<UploadResult | null>;
  onStop?: () => void;
  isRunning?: boolean;
  disabled?: boolean;
  /** True when the user has no agent available — disables the editor and
   *  surfaces a distinct placeholder. Kept separate from `disabled` so
   *  archived-session copy stays untouched. */
  noAgent?: boolean;
  /** Name of the currently selected agent, used in the placeholder. */
  agentName?: string;
  /** Agent id for draft scoping on brand-new (not-yet-created) chats. */
  agentId?: string | null;
  /** Rendered at the bottom-left of the input bar — typically the agent picker. */
  leftAdornment?: ReactNode;
  /** Chat @ suggestions: current/recent issue/project entries. */
  contextItems?: MentionItem[];
  /** Workspace id — scopes the project list query for the project picker. */
  wsId: string;
  /** Id of the active chat session, if any. Required to bind a project.
   *  Null for a brand-new (not-yet-created) chat: the project picker is
   *  hidden in that case since there's no session to attach the project to. */
  sessionId?: string | null;
  /** Project currently bound to the active session, if any. Drives the
   *  project picker's selected value. */
  currentProjectId?: string | null;
  /**
   * Fullscreen mobile sheet: pad the bottom with the device safe-area so the
   * composer isn't tucked under the home indicator / browser chrome.
   */
  safeArea?: boolean;
}

export function ChatInput({
  onSend,
  restoreDraftRequest,
  onRestoreDraftConsumed,
  onUploadFile,
  onStop,
  isRunning,
  disabled,
  noAgent,
  agentName,
  agentId,
  leftAdornment,
  contextItems,
  wsId,
  sessionId,
  currentProjectId,
  safeArea = false,
}: ChatInputProps) {
  const { t } = useT("chat");
  const editorRef = useRef<ContentEditorRef>(null);
  // ChatWindow always passes sessionId/agentId. Prefer those for draft
  // scoping so DM-bubble mode never bleeds into the global desktop chat store.
  // react-doctor-disable-next-line react-doctor/no-event-handler -- prop→local alias for draft/storage key only; not an event-handler-in-effect pattern
  const activeSessionId = sessionId ?? null;
  // react-doctor-disable-next-line react-doctor/no-event-handler -- same prop→local alias as above for per-agent new-chat draft slot
  const selectedAgentId = agentId ?? null;
  // Two keys with deliberately different concerns:
  //
  // `draftKey` — zustand storage key. Scopes the in-progress draft per
  // session so different sessions don't bleed text into each other; for
  // brand-new chats it falls back to a per-agent slot so switching agents
  // mid-compose gives each agent its own draft. This is a STORAGE key, not
  // a React identity.
  //
  // `editorKey` — React `key` on the ContentEditor. Used to force a
  // remount when the user explicitly switches agent (so Tiptap's
  // Placeholder, which only reads on mount, refreshes to "Tell {agent}…")
  // or when a cancelled empty run restores a draft from the server.
  // Crucially this does NOT include `activeSessionId`: when the user
  // uploads a file in a brand-new chat, `handleUploadFile` first awaits
  // `ensureSession` which lazily creates the session and flips
  // `activeSessionId` from null → uuid mid-upload. If the editor key
  // depended on session id, that flip would unmount the editor right as
  // the blob preview was inserted, dropping the in-progress upload's
  // image node before file-upload.ts could swap it for the CDN URL — the
  // user would see the image flash on then disappear. Keeping editor
  // identity stable across the lazy-create event is what makes
  // first-upload-creates-session work the same as second-upload.
  const draftKey =
    activeSessionId ?? `${DRAFT_NEW_SESSION}:${selectedAgentId ?? ""}`;
  // Select a primitive — empty-string fallback keeps referential stability.
  const inputDraft = useChatStore((s) => s.inputDrafts[draftKey] ?? "");
  const setInputDraft = useChatStore((s) => s.setInputDraft);
  const clearInputDraft = useChatStore((s) => s.clearInputDraft);
  const [isEmpty, setIsEmpty] = useState(!inputDraft.trim());
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [editorRestore, setEditorRestore] = useState<{
    id: string;
    content: string;
    draftKey: string;
  } | null>(null);
  const activeRestore = editorRestore?.draftKey === draftKey ? editorRestore : null;
  const editorKey = `${selectedAgentId ?? "no-agent"}:${activeRestore?.id ?? "base"}`;
  // Number of in-flight uploads. We track this explicitly (rather than
  // peeking at the editor on every render) so the SubmitButton visibly
  // disables the instant an upload starts and re-enables the instant it
  // finishes. handleSend ALSO checks `hasActiveUploads()` for paths that
  // bypass the button (Mod+Enter while paste is mid-stream, drag-drop
  // racing the keyboard) — defense in depth.
  const [pendingUploads, setPendingUploads] = useState(0);

  // Maps "URL inserted into the editor" → "attachment row id" so that
  // on send we can ask the server to bind only the attachments still
  // referenced in the message body. Cleared after every send. Mirrors
  // the comment-input flow exactly. The map key MUST match what the
  // editor actually wrote into the markdown — that's `markdownLink`
  // (the stable per-attachment URL) for normal post-MUL-3130 uploads
  // and `link` (= att.url) for the no-workspace upload branch where
  // there's no attachment-row id to address. Storing only `link` here
  // would cause `content.includes(url)` to miss every new chat upload
  // because the editor persists `markdownLink` instead, and the
  // `onSend` call would silently drop `attachment_ids` so the
  // attachment never binds to the chat message.
  const uploadMapRef = useRef<Map<string, string>>(new Map());

  useEffect(() => {
    if (!restoreDraftRequest) return;
    if (inputDraft.trim()) {
      logger.info("input.restore skipped: draft already has content", {
        draftKey,
        restoreId: restoreDraftRequest.id,
      });
      onRestoreDraftConsumed?.();
      return;
    }
    setInputDraft(draftKey, restoreDraftRequest.content);
    setIsEmpty(!restoreDraftRequest.content.trim());
    setEditorRestore({
      id: restoreDraftRequest.id,
      content: restoreDraftRequest.content,
      draftKey,
    });
    onRestoreDraftConsumed?.();
  }, [draftKey, inputDraft, onRestoreDraftConsumed, restoreDraftRequest, setInputDraft]);

  const handleUpload = useCallback(
    async (file: File): Promise<UploadResult | null> => {
      if (!onUploadFile) return null;
      setPendingUploads((n) => n + 1);
      try {
        const result = await onUploadFile(file);
        if (result) {
          const persistedURL = result.markdownLink || result.link;
          uploadMapRef.current.set(persistedURL, result.id);
        }
        return result;
      } finally {
        setPendingUploads((n) => Math.max(0, n - 1));
      }
    },
    [onUploadFile],
  );

  // Drop zone wraps the rounded card so a drop anywhere on the input
  // surface routes the file through the editor's upload extension (same
  // handler as the in-editor paste path).
  const { isDragOver, dropZoneProps } = useFileDropZone({
    onDrop: (files) => files.forEach((f) => editorRef.current?.uploadFile(f)),
  });

  const handleSend = async () => {
    const content = editorRef.current?.getMarkdown()?.replace(/(\n\s*)+$/, "").trim();
    if (!content || isSubmitting || disabled || noAgent) {
      logger.debug("input.send skipped", {
        emptyContent: !content,
        isRunning,
        isSubmitting,
        disabled,
        noAgent,
      });
      return;
    }
    // Block the send while any file is still uploading. If we let it
    // through the attachment id is not yet in uploadMapRef (the upload
    // resolves later) and the attachment would only end up bound to the
    // session, not the message — the agent then can't `multica attachment
    // download <id>` the file. The SubmitButton is also disabled in this
    // state via `uploading`, but Mod+Enter bypasses the button so we
    // still gate here.
    if (editorRef.current?.hasActiveUploads()) {
      logger.debug("input.send skipped: uploads in flight");
      return;
    }
    // Only send attachment IDs for uploads still present in the content.
    // Edits / deletions that remove the markdown URL also drop the binding.
    const activeIds: string[] = [];
    for (const [url, id] of uploadMapRef.current) {
      if (content.includes(url)) activeIds.push(id);
    }
    // Capture draft key BEFORE onSend — creating a new session mutates
    // activeSessionId synchronously, so reading it after onSend would point
    // at the new session and leave the old draft orphaned.
    const keyAtSend = draftKey;
    logger.info("input.send", {
      contentLength: content.length,
      draftKey: keyAtSend,
      attachmentCount: activeIds.length,
    });
    setIsSubmitting(true);
    let accepted: void | boolean;
    try {
      accepted = await onSend(content, activeIds.length > 0 ? activeIds : undefined);
    } catch (err) {
      logger.warn("input.send failed", err);
      setIsSubmitting(false);
      return;
    }
    setIsSubmitting(false);
    if (accepted === false) return;
    editorRef.current?.clearContent();
    // Drop focus so the caret doesn't keep blinking under the StatusPill /
    // streaming reply that's about to take over the user's attention. The
    // input is also `disabled` once isRunning flips, and a focused-but-
    // disabled editor reads as a stale cursor. We deliberately don't auto-
    // refocus on completion — that would interrupt the user if they're
    // selecting text from the assistant reply; one click to refocus is
    // a fair price for not stealing focus mid-action.
    editorRef.current?.blur();
    clearInputDraft(keyAtSend);
    uploadMapRef.current.clear();
    setIsEmpty(true);
  };

  const placeholder = noAgent
    ? t(($) => $.input.placeholder_no_agent)
    : disabled
      ? t(($) => $.input.placeholder_archived)
      : agentName
        ? t(($) => $.input.placeholder_named, { name: agentName })
        : t(($) => $.input.placeholder_default);

  const uploadEnabled = !!onUploadFile && !disabled && !noAgent;

  return (
    <div
      className={cn(
        "shrink-0 px-4 pt-0 sm:px-5",
        safeArea
          ? "pb-[max(0.75rem,env(safe-area-inset-bottom))]"
          : "pb-3",
        // Outer wrapper carries the disabled cursor. Inner card sets
        // pointer-events-none, which suppresses hover (and therefore
        // any cursor of its own) — splitting the two layers lets hover
        // bubble back here so the browser actually reads cursor.
        noAgent && "cursor-not-allowed",
      )}
    >
      <div
        {...(uploadEnabled ? dropZoneProps : {})}
        className={cn(
          "relative mx-auto flex min-h-16 max-h-40 w-full max-w-4xl flex-col rounded-lg bg-card pb-9 border-1 border-border transition-colors focus-within:border-brand",
          // Visual + interaction lock when there's no agent. We don't
          // toggle ContentEditor's editable mode (Tiptap can't switch
          // cleanly post-mount, and the prop has been removed); instead
          // we drop pointer events at the wrapper level so clicks miss
          // the editor entirely, and dim the surface so it reads as
          // "disabled" rather than "broken".
          noAgent && "pointer-events-none opacity-60",
        )}
        aria-disabled={noAgent || undefined}
      >
        <div className="flex-1 min-h-0 overflow-y-auto px-3 py-2">
          <ContentEditor
            // See the editorKey / draftKey split note above — editorKey
            // intentionally does not depend on activeSessionId.
            key={editorKey}
            ref={editorRef}
            defaultValue={activeRestore?.content ?? inputDraft}
            // Chat keeps typed/loaded bare URLs as PLAIN TEXT in the input
            // (#531/#542) — they are made clickable on the read side, not here.
            plainUrls
            placeholder={placeholder}
            onUpdate={(md) => {
              setIsEmpty(!md.trim());
              setInputDraft(draftKey, md);
            }}
            onSubmit={handleSend}
            onUploadFile={uploadEnabled ? handleUpload : undefined}
            debounceMs={100}
            mentionMode={contextItems ? "context" : "default"}
            mentionContextItems={contextItems}
            enableSlashCommands
            // Chat is short-form — the floating formatting toolbar is
            // more distraction than feature here.
            showBubbleMenu={false}
            editorMentionVariant="plain"
            // Chat / FAB bubble: bare Enter sends (same as channel/DM
            // composers). Shift+Enter still inserts a newline via Tiptap's
            // default; code blocks keep Enter for a new line inside the fence.
            // IME composition is guarded in submit-shortcut.
            submitOnEnter
          />
        </div>
        {leftAdornment && (
          <div className="absolute bottom-1.5 left-2 flex items-center">
            {leftAdornment}
          </div>
        )}
        <div className="absolute bottom-1 right-1.5 flex items-center gap-1">
          {sessionId && (
            <ChatProjectPicker
              wsId={wsId}
              sessionId={sessionId}
              currentProjectId={currentProjectId ?? null}
              disabled={!!disabled || !!noAgent}
            />
          )}
          {uploadEnabled && (
            <FileUploadButton
              size="sm"
              onSelect={(file) => editorRef.current?.uploadFile(file)}
            />
          )}
          {isRunning && onStop && (
            <SubmitButton
              onClick={() => {}}
              running
              onStop={onStop}
              stopTooltip={t(($) => $.input.stop_tooltip)}
            />
          )}
          <SubmitButton
            onClick={handleSend}
            disabled={isEmpty || isSubmitting || !!disabled || !!noAgent || pendingUploads > 0}
            running={isRunning}
            allowSubmitWhileRunning
            tooltip={`${t(($) => $.input.send_tooltip)} · ${enterKey}`}
          />
        </div>
        {uploadEnabled && isDragOver && <FileDropOverlay />}
      </div>
    </div>
  );
}

/**
 * Chat-side adapter around the shared ProjectPickerButton: owns the
 * session-project mutation so the presentational button stays free of
 * business logic. Only mounted when a session exists (gated by the caller),
 * so the mutation/query hooks never run for brand-new, not-yet-created chats.
 */
function ChatProjectPicker({
  wsId,
  sessionId,
  currentProjectId,
  disabled,
}: {
  wsId: string;
  sessionId: string;
  currentProjectId: string | null;
  disabled: boolean;
}) {
  const { t } = useT("chat");
  const setProject = useSetChatSessionProject();

  return (
    <ProjectPickerButton
      wsId={wsId}
      value={currentProjectId}
      onChange={(projectId) => setProject.mutate({ sessionId, projectId })}
      disabled={disabled}
      label={t(($) => $.input.project_label)}
      noneLabel={t(($) => $.input.project_none)}
      tooltip={t(($) => $.input.project_tooltip)}
    />
  );
}
