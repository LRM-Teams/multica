"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { computePosition, offset, flip, shift, autoUpdate } from "@floating-ui/dom";
import type { Editor } from "@tiptap/core";
import { Slice } from "@tiptap/pm/model";
import { Button } from "@multica/ui/components/ui/button";
import type { NoteAIEditResult } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ArrowUp, Loader2, Sparkles } from "lucide-react";
import { useT } from "../i18n";

const PAGE_AI_CONTEXT_CHARS = 2400;
const PROMPT_MAX_HEIGHT_PX = 96;

export type PageEditAIRequest = {
  instruction: string;
  content: string;
  contextBefore: string;
  contextAfter: string;
};

export type PageEditAIAction = (request: PageEditAIRequest, options?: { signal?: AbortSignal }) => Promise<NoteAIEditResult>;

export type EmptyLineAiState = {
  status: "prompt" | "loading" | "review";
  from: number;
  to: number;
  caretPos: number;
  anchorRect: DOMRect;
  instruction: string;
  result?: NoteAIEditResult;
};

function replaceRangeWithMarkdown(editor: Editor, from: number, to: number, markdown: string) {
  const safeFrom = Math.max(0, Math.min(from, editor.state.doc.content.size));
  const safeTo = Math.max(safeFrom, Math.min(to, editor.state.doc.content.size));
  if (!editor.markdown) {
    editor.chain().focus().command(({ tr }) => {
      tr.insertText(markdown, safeFrom, safeTo);
      return true;
    }).run();
    return;
  }
  const json = editor.markdown.parse(markdown);
  const node = editor.schema.nodeFromJSON(json);
  const slice = Slice.maxOpen(node.content);
  editor.chain().focus().command(({ tr }) => {
    tr.replaceRange(safeFrom, safeTo, slice);
    return true;
  }).run();
}

function replaceDocumentWithMarkdown(editor: Editor, markdown: string) {
  if (editor.markdown) {
    editor.commands.setContent(markdown, { contentType: "markdown" });
    return;
  }
  editor.commands.setContent(markdown);
}

function patchedDocumentMarkdown(current: string, result: NoteAIEditResult) {
  const target = result.target?.trim();
  if (!target) return null;
  const source = current.trimEnd();
  if (source.includes(target)) return source.replace(target, result.markdown);
  const looseTarget = target.trim();
  if (looseTarget && source.includes(looseTarget)) return source.replace(looseTarget, result.markdown);
  return null;
}

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

function resizePromptField(el: HTMLTextAreaElement | null) {
  if (!el) return;
  el.style.height = "0px";
  el.style.height = `${Math.min(el.scrollHeight, PROMPT_MAX_HEIGHT_PX)}px`;
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
  onApplyTitle,
  onClose,
}: {
  editor: Editor;
  state: EmptyLineAiState;
  onChange: (state: EmptyLineAiState) => void;
  onEditPageWithAI: PageEditAIAction;
  onApplyTitle?: (title: string) => void;
  onClose: () => void;
}) {
  const { t } = useT("editor");
  const floatingRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const closedRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);
  const loading = state.status === "loading";
  const canSubmit = !loading;

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
    resizePromptField(textareaRef.current);
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
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      const root = floatingRef.current;
      if (!root) return;
      if (root.contains(event.target as Node)) return;
      close();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => document.removeEventListener("pointerdown", onPointerDown, true);
  }, [close]);

  const submit = useCallback(async () => {
    if (state.status === "loading" || closedRef.current) return;
    const request = buildRequest(editor, state);
    const abort = new AbortController();
    abortRef.current = abort;
    onChange({ ...state, status: "loading", instruction: request.instruction });
    try {
      const result = await onEditPageWithAI(request, { signal: abort.signal });
      // Ignore late results if the user already dismissed the prompt.
      if (!closedRef.current) {
        if (!result.markdown.trim()) throw new Error(t(($) => $.page_ai.empty_result));
        onChange({ ...state, status: "review", instruction: request.instruction, result });
      }
    } catch (error) {
      if (!closedRef.current) {
        onChange({ ...state, status: "prompt" });
        showErrorToast(
          error instanceof Error && error.name !== "AbortError" && error.message
            ? error.message
            : t(($) => $.page_ai.failed),
        );
      }
    } finally {
      if (abortRef.current === abort) abortRef.current = null;
    }
  }, [editor, onChange, onEditPageWithAI, state, t]);

  const applyTitle = (result: NoteAIEditResult) => {
    if (result.title) onApplyTitle?.(result.title);
  };

  const insertHere = () => {
    const result = state.result;
    if (!result) return;
    replaceRangeWithMarkdown(editor, state.from, state.to, result.markdown);
    applyTitle(result);
    close();
  };

  const replacePage = () => {
    const result = state.result;
    if (!result) return;
    replaceDocumentWithMarkdown(editor, result.markdown);
    applyTitle(result);
    close();
  };

  const applyPatch = () => {
    const result = state.result;
    if (!result) return;
    const patched = patchedDocumentMarkdown(editor.getMarkdown(), result);
    if (!patched) {
      showErrorToast(t(($) => $.page_ai.patch_target_missing));
      return;
    }
    replaceDocumentWithMarkdown(editor, patched);
    applyTitle(result);
    close();
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
      style={{ position: "fixed", zIndex: 55 }}
      className="w-[min(560px,calc(100vw-32px))]"
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
            <div className="grid max-h-72 gap-2 overflow-y-auto p-3.5 md:grid-cols-2">
              <div>
                <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t(($) => $.page_ai.current_fragment)}</div>
                <div className="whitespace-pre-wrap rounded-lg bg-muted/60 p-2.5 text-xs leading-5 text-muted-foreground">{reviewResult.target}</div>
              </div>
              <div>
                <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t(($) => $.page_ai.replacement_fragment)}</div>
                <div className="whitespace-pre-wrap rounded-lg bg-primary/10 p-2.5 text-xs leading-5">{reviewResult.markdown}</div>
              </div>
            </div>
          ) : reviewResult.action === "replace_page" ? (
            <div className="grid max-h-72 gap-2 overflow-y-auto p-3.5 md:grid-cols-2">
              <div>
                <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t(($) => $.page_ai.current_page)}</div>
                <div className="whitespace-pre-wrap rounded-lg bg-muted/60 p-2.5 text-xs leading-5 text-muted-foreground">{currentMarkdown || "(empty)"}</div>
              </div>
              <div>
                <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t(($) => $.page_ai.proposed_page)}</div>
                <div className="whitespace-pre-wrap rounded-lg bg-primary/10 p-2.5 text-xs leading-5">{reviewResult.markdown}</div>
              </div>
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
              "min-h-8 max-h-24 flex-1 resize-none bg-transparent py-1.5 pr-1 text-sm leading-5 text-foreground outline-none",
              "placeholder:text-muted-foreground disabled:opacity-70",
            )}
          />
          <button
            type="button"
            onClick={() => void submit()}
            disabled={!canSubmit}
            aria-label={t(($) => $.page_ai.send)}
            title={t(($) => $.page_ai.hint)}
            className={cn(
              "mb-0.5 flex size-8 shrink-0 items-center justify-center rounded-full transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-popover",
              loading
                ? "bg-muted text-muted-foreground"
                : "bg-foreground text-background hover:bg-foreground/90",
            )}
          >
            {loading ? <Loader2 className="size-3.5 animate-spin" /> : <ArrowUp className="size-3.5" />}
          </button>
        </div>
      )}
    </div>
  );
}

export { EmptyLineAiMenu };
