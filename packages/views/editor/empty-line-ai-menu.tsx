"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { computePosition, offset, flip, shift, autoUpdate } from "@floating-ui/dom";
import type { Editor } from "@tiptap/core";
import { Slice } from "@tiptap/pm/model";
import { Button } from "@multica/ui/components/ui/button";
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

export type PageEditAIAction = (request: PageEditAIRequest) => Promise<string>;

export type EmptyLineAiState = {
  status: "prompt" | "loading" | "review";
  from: number;
  to: number;
  caretPos: number;
  anchorRect: DOMRect;
  instruction: string;
  result?: string;
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
  onClose,
}: {
  editor: Editor;
  state: EmptyLineAiState;
  onChange: (state: EmptyLineAiState) => void;
  onEditPageWithAI: PageEditAIAction;
  onClose: () => void;
}) {
  const { t } = useT("editor");
  const floatingRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const closedRef = useRef(false);
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
    onClose();
  }, [onClose]);

  const closeAndInsertSpace = useCallback(() => {
    closedRef.current = true;
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
    onChange({ ...state, status: "loading", instruction: request.instruction });
    try {
      const result = (await onEditPageWithAI(request)).trim();
      if (closedRef.current) return;
      if (!result) throw new Error(t(($) => $.page_ai.empty_result));
      onChange({ ...state, status: "review", instruction: request.instruction, result });
    } catch (error) {
      if (closedRef.current) return;
      onChange({ ...state, status: "prompt" });
      showErrorToast(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.page_ai.failed),
      );
    }
  }, [editor, onChange, onEditPageWithAI, state, t]);

  const insertHere = () => {
    replaceRangeWithMarkdown(editor, state.from, state.to, state.result ?? "");
    close();
  };

  const replacePage = () => {
    replaceDocumentWithMarkdown(editor, state.result ?? "");
    close();
  };

  return (
    <div
      ref={floatingRef}
      data-testid="empty-line-ai-menu"
      style={{ position: "fixed", zIndex: 55 }}
      className="w-[min(560px,calc(100vw-32px))]"
      onMouseDown={(event) => event.stopPropagation()}
    >
      {state.status === "review" ? (
        <div className="overflow-hidden rounded-2xl border bg-popover text-popover-foreground shadow-lg">
          <div className="flex items-center gap-2 border-b px-3.5 py-2.5">
            <span className="flex size-6 items-center justify-center rounded-full bg-muted text-foreground/80">
              <Sparkles className="size-3.5" />
            </span>
            <span className="text-xs font-medium text-muted-foreground">{t(($) => $.page_ai.result_title)}</span>
          </div>
          <div className="max-h-64 overflow-y-auto whitespace-pre-wrap px-3.5 py-3 text-sm leading-6">
            {state.result}
          </div>
          <div className="flex flex-wrap justify-end gap-1.5 border-t px-2.5 py-2">
            <Button size="sm" variant="ghost" onClick={close}>{t(($) => $.page_ai.discard)}</Button>
            <Button size="sm" variant="outline" onClick={replacePage}>{t(($) => $.page_ai.replace_page)}</Button>
            <Button size="sm" onClick={insertHere}>{t(($) => $.page_ai.insert)}</Button>
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
