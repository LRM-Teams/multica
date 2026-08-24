"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { computePosition, offset, flip, shift, autoUpdate } from "@floating-ui/dom";
import type { Editor } from "@tiptap/core";
import { Button } from "@multica/ui/components/ui/button";
import type { NoteAIEditResult, NoteAIJobStatus } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ArrowUp, Sparkles, Square } from "lucide-react";
import { useT } from "../i18n";
import { NoteAIDiffPreview } from "./note-ai-diff";
import { patchedDocumentMarkdown, replaceRangeWithMarkdown } from "./utils/note-ai-apply";
import { captureNoteAIApplyDiagnostic } from "./utils/note-ai-apply-diagnostics";
import {
  captureNoteAIUndoSnapshot,
  setEditorMarkdown,
  showNoteAIApplyUndoToast,
} from "./utils/note-ai-apply-undo";

const PAGE_AI_CONTEXT_CHARS = 2400;
/** Default prompt shell width — longer than the old 560px bar. */
const PROMPT_MIN_WIDTH_PX = 720;
const PROMPT_VIEWPORT_GUTTER_PX = 32;
/** Left sparkles chip + gaps + right action button + horizontal padding. */
const PROMPT_CHROME_PX = 88;

export type PageEditAIRequest = {
  instruction: string;
  content: string;
  contextBefore: string;
  contextAfter: string;
};

export type PageEditAIAction = (request: PageEditAIRequest, options?: { signal?: AbortSignal; onStatus?: (status: NoteAIJobStatus) => void }) => Promise<NoteAIEditResult>;

export type EmptyLineAiState = {
  status: "prompt" | "loading" | "review";
  from: number;
  to: number;
  caretPos: number;
  anchorRect: DOMRect;
  instruction: string;
  jobStatus?: NoteAIJobStatus;
  cancelling?: boolean;
  result?: NoteAIEditResult;
};

function buildRequest(editor: Editor, state: EmptyLineAiState): PageEditAIRequest {
  const docSize = editor.state.doc.content.size;
  const from = Math.max(0, Math.min(state.from, docSize));
  const to = Math.max(from, Math.min(state.to, docSize));
  return {
    instruction: state.instruction.trim(),
    content: editor.getMarkdown().trimEnd(),
    contextBefore: editor.state.doc.textBetween(Math.max(0, from - PAGE_AI_CONTEXT_CHARS), from, "\n", "\n").trim(),
    contextAfter: editor.state.doc.textBetween(to, Math.min(docSize, to + PAGE_AI_CONTEXT_CHARS), "\n", "\n").trim(),
  };
}

function measurePromptContentWidth(textarea: HTMLTextAreaElement): number {
  const styles = window.getComputedStyle(textarea);
  const mirror = document.createElement("div");
  mirror.setAttribute("aria-hidden", "true");
  mirror.style.cssText = [
    "position:absolute",
    "left:-9999px",
    "top:0",
    "visibility:hidden",
    "white-space:pre",
    "height:auto",
    "width:auto",
    `font:${styles.font}`,
    `letter-spacing:${styles.letterSpacing}`,
    `text-transform:${styles.textTransform}`,
    `padding:${styles.paddingTop} ${styles.paddingRight} ${styles.paddingBottom} ${styles.paddingLeft}`,
    `border-left-width:${styles.borderLeftWidth}`,
    `border-right-width:${styles.borderRightWidth}`,
    `box-sizing:${styles.boxSizing}`,
  ].join(";");
  // Prefer live value; fall back to placeholder so empty prompts keep the default width.
  mirror.textContent = textarea.value.length > 0 ? textarea.value : (textarea.placeholder || " ");
  document.body.appendChild(mirror);
  const width = mirror.scrollWidth;
  mirror.remove();
  return width;
}

/**
 * Grow the floating shell wider as the instruction lengthens (up to the
 * viewport), then size the textarea height to its full scrollHeight so the
 * field never needs an inner scrollbar.
 */
function resizePromptShell(textarea: HTMLTextAreaElement | null, shell: HTMLElement | null) {
  if (!textarea || !shell) return;
  const maxWidth = Math.max(280, window.innerWidth - PROMPT_VIEWPORT_GUTTER_PX);
  const minWidth = Math.min(PROMPT_MIN_WIDTH_PX, maxWidth);
  const contentWidth = measurePromptContentWidth(textarea);
  const desiredWidth = Math.min(maxWidth, Math.max(minWidth, contentWidth + PROMPT_CHROME_PX));
  shell.style.width = `${desiredWidth}px`;

  textarea.style.overflow = "hidden";
  textarea.style.height = "0px";
  textarea.style.height = `${textarea.scrollHeight}px`;
}

function insertSpaceAtCaret(editor: Editor, caretPos: number) {
  const docSize = editor.state.doc.content.size;
  const pos = Math.max(0, Math.min(caretPos, docSize));
  editor.chain().focus().insertContentAt(pos, " ").run();
}

function EmptyLineAiMenu({
  editor,
  state,
  onChange,
  onEditPageWithAI,
  onRequestPageAI,
  onApplyTitle,
  currentTitle,
  onClose,
}: {
  editor: Editor;
  state: EmptyLineAiState;
  onChange: (state: EmptyLineAiState) => void;
  onEditPageWithAI: PageEditAIAction;
  onRequestPageAI?: () => boolean;
  onApplyTitle?: (title: string) => void;
  currentTitle?: string;
  onClose: () => void;
}) {
  const { t } = useT("editor");
  const floatingRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const closedRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);
  const loading = state.status === "loading";
  const statusRef = useRef(state.status);
  statusRef.current = state.status;

  const virtualRef = useMemo(
    () => ({
      getBoundingClientRect: () => state.anchorRect,
      contextElement: editor.view.dom,
    }),
    [editor, state.anchorRect],
  );

  const close = useCallback(() => {
    closedRef.current = true;
    abortRef.current?.abort();
    onClose();
  }, [onClose]);

  const closeAndInsertSpace = useCallback(() => {
    closedRef.current = true;
    abortRef.current?.abort();
    onClose();
    insertSpaceAtCaret(editor, state.caretPos);
  }, [editor, onClose, state.caretPos]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!closedRef.current) textareaRef.current?.focus();
    }, 0);
    return () => window.clearTimeout(timer);
  }, []);

  useLayoutEffect(() => {
    if (state.status === "review") return;
    resizePromptShell(textareaRef.current, floatingRef.current);
  }, [state.instruction, state.status]);

  useEffect(() => {
    const el = floatingRef.current;
    if (!el) return;
    const updatePosition = () => {
      computePosition(virtualRef, el, {
        strategy: "fixed",
        placement: "bottom-start",
        middleware: [offset(10), flip(), shift({ padding: 12 })],
      }).then(({ x, y }) => {
        if (!el.isConnected) return;
        el.style.left = `${x}px`;
        el.style.top = `${y}px`;
      });
    };
    const cleanup = autoUpdate(virtualRef, el, updatePosition);
    return cleanup;
  }, [virtualRef, state.status]);

  // Esc dismisses regardless of which element currently has focus.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      close();
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [close]);

  // Click / tap outside the floating bar dismisses it.
  // While a job is in flight, outside clicks must not abort — only the stop
  // control cancels. Accidental editor clicks were cancelling slow replies.
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      const root = floatingRef.current;
      if (!root) return;
      if (root.contains(event.target as Node)) return;
      if (statusRef.current === "loading") return;
      close();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => document.removeEventListener("pointerdown", onPointerDown, true);
  }, [close]);

  const submit = useCallback(async () => {
    if (state.status === "loading" || closedRef.current) return;
    if (onRequestPageAI && !onRequestPageAI()) {
      close();
      return;
    }
    const request = buildRequest(editor, state);
    const abort = new AbortController();
    abortRef.current = abort;
    const loadingState = { ...state, status: "loading" as const, instruction: request.instruction, jobStatus: "queued" as const, cancelling: false };
    onChange(loadingState);
    try {
      const result = await onEditPageWithAI(request, {
        signal: abort.signal,
        onStatus: (jobStatus) => onChange({ ...loadingState, jobStatus }),
      });
      // Ignore late results if the user already dismissed the prompt.
      if (!closedRef.current) {
        if (!result.markdown.trim()) throw new Error(t(($) => $.page_ai.empty_result));
        onChange({ ...state, status: "review", instruction: request.instruction, result });
      }
    } catch (error) {
      if (!closedRef.current) {
        onChange({ ...state, status: "prompt" });
        if (error instanceof Error && error.name === "AbortError") return;
        showErrorToast(
          error instanceof Error && error.message
            ? error.message
            : t(($) => $.page_ai.failed),
        );
      }
    } finally {
      if (abortRef.current === abort) abortRef.current = null;
    }
  }, [close, editor, onChange, onEditPageWithAI, onRequestPageAI, state, t]);

  const cancelLoading = () => {
    if (state.status !== "loading") return;
    onChange({ ...state, cancelling: true });
    abortRef.current?.abort();
  };

  const finishApply = (result: NoteAIEditResult, snapshot: ReturnType<typeof captureNoteAIUndoSnapshot>) => {
    if (result.title) onApplyTitle?.(result.title);
    captureNoteAIApplyDiagnostic({ surface: "page", outcome: "applied", result });
    showNoteAIApplyUndoToast({
      editor,
      snapshot,
      onApplyTitle,
      message: t(($) => $.page_ai.applied),
      undoLabel: t(($) => $.page_ai.undo),
      onUndo: () => captureNoteAIApplyDiagnostic({ surface: "page", outcome: "undo_clicked", result }),
    });
    close();
  };

  const insertHere = () => {
    const result = state.result;
    if (!result) return;
    const snapshot = captureNoteAIUndoSnapshot(editor, result.title ? currentTitle : undefined);
    try {
      replaceRangeWithMarkdown(editor, state.from, state.to, result.markdown);
    } catch {
      captureNoteAIApplyDiagnostic({ surface: "page", outcome: "invalid_markdown", result });
      showErrorToast(t(($) => $.page_ai.invalid_markdown));
      return;
    }
    finishApply(result, snapshot);
  };

  const replacePage = () => {
    const result = state.result;
    if (!result) return;
    const snapshot = captureNoteAIUndoSnapshot(editor, result.title ? currentTitle : undefined);
    try {
      setEditorMarkdown(editor, result.markdown);
    } catch {
      captureNoteAIApplyDiagnostic({ surface: "page", outcome: "invalid_markdown", result });
      showErrorToast(t(($) => $.page_ai.invalid_markdown));
      return;
    }
    finishApply(result, snapshot);
  };

  const applyPatch = () => {
    const result = state.result;
    if (!result) return;
    const snapshot = captureNoteAIUndoSnapshot(editor, result.title ? currentTitle : undefined);
    const patched = patchedDocumentMarkdown(snapshot.markdown, result);
    if (!patched) {
      captureNoteAIApplyDiagnostic({ surface: "page", outcome: "patch_target_missing", result });
      showErrorToast(t(($) => $.page_ai.patch_target_missing));
      return;
    }
    try {
      setEditorMarkdown(editor, patched);
    } catch {
      captureNoteAIApplyDiagnostic({ surface: "page", outcome: "invalid_markdown", result });
      showErrorToast(t(($) => $.page_ai.invalid_markdown));
      return;
    }
    finishApply(result, snapshot);
  };

  const copyPatch = () => {
    const markdown = state.result?.markdown;
    if (!markdown || typeof navigator === "undefined") return;
    void navigator.clipboard?.writeText(state.result?.target ? `${state.result.target}\n---\n${markdown}` : markdown);
  };

  const reviewResult = state.status === "review" ? state.result : null;
  const currentMarkdown = reviewResult ? editor.getMarkdown().trimEnd() : "";
  const reviewTitle = reviewResult?.action === "insert"
    ? t(($) => $.page_ai.action_insert)
    : reviewResult?.action === "replace_selection"
      ? t(($) => $.page_ai.action_replace_selection)
      : reviewResult?.action === "replace_page"
        ? t(($) => $.page_ai.action_replace_page)
        : reviewResult?.action === "patch"
          ? t(($) => $.page_ai.action_patch)
          : "";

  return (
    <div
      ref={floatingRef}
      data-testid="empty-line-ai-menu"
      style={{
        position: "fixed",
        zIndex: 55,
        width: state.status === "review"
          ? undefined
          : `min(${PROMPT_MIN_WIDTH_PX}px, calc(100vw - ${PROMPT_VIEWPORT_GUTTER_PX}px))`,
      }}
      className={state.status === "review" ? "w-[min(720px,calc(100vw-32px))]" : undefined}
      onMouseDown={(event) => event.stopPropagation()}
    >
      {reviewResult ? (
        <div className="overflow-hidden rounded-2xl border bg-popover text-popover-foreground shadow-lg">
          <div className="flex items-center gap-2 border-b px-3.5 py-2.5">
            <span className="flex size-6 items-center justify-center rounded-full bg-muted text-foreground/80">
              <Sparkles className="size-3.5" />
            </span>
            <div className="min-w-0">
              <div className="text-xs font-medium text-muted-foreground">{reviewTitle}</div>
              {reviewResult.rationale && <div className="mt-0.5 truncate text-[11px] text-muted-foreground/80">{reviewResult.rationale}</div>}
            </div>
          </div>
          {reviewResult.title && (
            <div className="border-b px-3.5 py-2 text-xs text-muted-foreground">
              {t(($) => $.page_ai.title_suggestion)} <span className="font-medium text-foreground">{reviewResult.title}</span>
            </div>
          )}
          {reviewResult.action === "patch" && reviewResult.target ? (
            <div className="p-3.5">
              <NoteAIDiffPreview
                before={reviewResult.target}
                after={reviewResult.markdown}
                beforeLabel={t(($) => $.page_ai.current_fragment)}
                afterLabel={t(($) => $.page_ai.replacement_fragment)}
                emptyLabel={t(($) => $.page_ai.no_diff)}
                omittedLabel={t(($) => $.page_ai.diff_omitted)}
              />
            </div>
          ) : reviewResult.action === "replace_page" ? (
            <div className="p-3.5">
              <NoteAIDiffPreview
                before={currentMarkdown}
                after={reviewResult.markdown}
                beforeLabel={t(($) => $.page_ai.current_page)}
                afterLabel={t(($) => $.page_ai.proposed_page)}
                emptyLabel={t(($) => $.page_ai.no_diff)}
                omittedLabel={t(($) => $.page_ai.diff_omitted)}
              />
            </div>
          ) : (
            <div className="max-h-64 overflow-y-auto whitespace-pre-wrap px-3.5 py-3 text-sm leading-6">
              {reviewResult.markdown}
            </div>
          )}
          <div className="flex flex-wrap justify-end gap-1.5 border-t px-2.5 py-2">
            <Button size="sm" variant="ghost" onClick={close}>{t(($) => $.page_ai.discard)}</Button>
            {reviewResult.action === "patch" && <Button size="sm" variant="outline" onClick={copyPatch}>{t(($) => $.page_ai.copy_patch)}</Button>}
            {reviewResult.action === "replace_page" ? (
              <Button size="sm" onClick={replacePage}>{t(($) => $.page_ai.replace_page)}</Button>
            ) : reviewResult.action === "patch" ? (
              <Button size="sm" onClick={applyPatch}>{t(($) => $.page_ai.apply_patch)}</Button>
            ) : (
              <Button size="sm" onClick={insertHere}>{reviewResult.action === "insert" ? t(($) => $.page_ai.insert) : t(($) => $.page_ai.replace_selection)}</Button>
            )}
          </div>
        </div>
      ) : (
        <div className="flex items-end gap-2 rounded-[26px] border bg-popover px-2 py-1.5 text-popover-foreground shadow-lg">
          <span
            className="mb-0.5 flex size-8 shrink-0 items-center justify-center rounded-full border bg-background text-foreground/80"
            aria-hidden="true"
          >
            <Sparkles className="size-3.5" />
          </span>
          <textarea
            ref={textareaRef}
            value={state.instruction}
            disabled={loading}
            rows={1}
            onChange={(event) => onChange({ ...state, instruction: event.target.value })}
            onKeyDown={(event) => {
              // Empty prompt + Space = user wants a literal space in the note,
              // not a leading space in the AI instruction field.
              if ((event.key === " " || event.code === "Space") && state.instruction.length === 0) {
                event.preventDefault();
                closeAndInsertSpace();
                return;
              }
              if (event.key === "Escape") {
                event.preventDefault();
                close();
                return;
              }
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void submit();
              }
            }}
            placeholder={t(($) => $.page_ai.instruction_placeholder)}
            aria-label={t(($) => $.page_ai.trigger_label)}
            title={t(($) => $.page_ai.hint)}
            className={cn(
              "min-h-8 flex-1 resize-none overflow-hidden bg-transparent py-1.5 pr-1 text-sm leading-5 text-foreground outline-none",
              "placeholder:text-muted-foreground disabled:opacity-70",
            )}
          />
          <button
            type="button"
            onClick={() => {
              if (loading) {
                cancelLoading();
                return;
              }
              void submit();
            }}
            disabled={loading && state.cancelling}
            aria-label={loading ? t(($) => $.page_ai.cancel) : t(($) => $.page_ai.send)}
            aria-busy={loading || undefined}
            title={loading ? t(($) => $.page_ai.cancel) : t(($) => $.page_ai.hint)}
            className={cn(
              "mb-0.5 flex size-8 shrink-0 items-center justify-center rounded-full transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-popover",
              "bg-foreground text-background hover:bg-foreground/90",
              loading && "animate-pulse",
            )}
          >
            {loading ? <Square className="size-2.5 fill-current" /> : <ArrowUp className="size-3.5" />}
          </button>
        </div>
      )}
    </div>
  );
}

export { EmptyLineAiMenu };
