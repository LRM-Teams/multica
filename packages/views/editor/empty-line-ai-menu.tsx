"use client";

import { useCallback, useEffect, useMemo, useRef } from "react";
import { computePosition, offset, flip, shift, autoUpdate } from "@floating-ui/dom";
import type { Editor } from "@tiptap/core";
import { Slice } from "@tiptap/pm/model";
import { Button } from "@multica/ui/components/ui/button";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Loader2, Sparkles } from "lucide-react";
import { useT } from "../i18n";

const PAGE_AI_CONTEXT_CHARS = 2400;

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

  const virtualRef = useMemo(
    () => ({
      getBoundingClientRect: () => state.anchorRect,
      contextElement: editor.view.dom,
    }),
    [editor, state.anchorRect],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => textareaRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    const el = floatingRef.current;
    if (!el) return;
    const updatePosition = () => {
      computePosition(virtualRef, el, {
        strategy: "fixed",
        placement: "bottom-start",
        middleware: [offset(8), flip(), shift({ padding: 12 })],
      }).then(({ x, y }) => {
        if (!el.isConnected) return;
        el.style.left = `${x}px`;
        el.style.top = `${y}px`;
      });
    };
    const cleanup = autoUpdate(virtualRef, el, updatePosition);
    return cleanup;
  }, [virtualRef]);

  const submit = useCallback(async () => {
    if (state.status === "loading") return;
    const request = buildRequest(editor, state);
    onChange({ ...state, status: "loading", instruction: request.instruction });
    try {
      const result = (await onEditPageWithAI(request)).trim();
      if (!result) throw new Error(t(($) => $.page_ai.empty_result));
      onChange({ ...state, status: "review", instruction: request.instruction, result });
    } catch (error) {
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
    onClose();
  };

  const replacePage = () => {
    replaceDocumentWithMarkdown(editor, state.result ?? "");
    onClose();
  };

  return (
    <div
      ref={floatingRef}
      style={{ position: "fixed", zIndex: 55 }}
      className="w-[min(520px,calc(100vw-32px))] rounded-2xl border bg-popover p-3 text-popover-foreground shadow-xl"
      onMouseDown={(event) => event.stopPropagation()}
    >
      {state.status === "review" ? (
        <>
          <div className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
            <Sparkles className="size-3.5" />
            {t(($) => $.page_ai.result_title)}
          </div>
          <div className="max-h-64 overflow-y-auto whitespace-pre-wrap rounded-xl bg-muted/60 p-3 text-sm leading-6">
            {state.result}
          </div>
          <div className="mt-3 flex flex-wrap justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={onClose}>{t(($) => $.page_ai.discard)}</Button>
            <Button size="sm" variant="outline" onClick={replacePage}>{t(($) => $.page_ai.replace_page)}</Button>
            <Button size="sm" onClick={insertHere}>{t(($) => $.page_ai.insert)}</Button>
          </div>
        </>
      ) : (
        <>
          <div className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
            <Sparkles className="size-3.5" />
            {t(($) => $.page_ai.trigger_label)}
          </div>
          <textarea
            ref={textareaRef}
            value={state.instruction}
            disabled={state.status === "loading"}
            onChange={(event) => onChange({ ...state, instruction: event.target.value })}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault();
                onClose();
                return;
              }
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void submit();
              }
            }}
            placeholder={t(($) => $.page_ai.instruction_placeholder)}
            aria-label={t(($) => $.page_ai.trigger_label)}
            className="min-h-24 w-full resize-none rounded-xl border bg-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring disabled:opacity-70"
          />
          <div className="mt-3 flex items-center justify-between gap-3">
            <div className="text-xs text-muted-foreground">{t(($) => $.page_ai.hint)}</div>
            <div className="flex gap-2">
              <Button size="sm" variant="ghost" onClick={onClose} disabled={state.status === "loading"}>{t(($) => $.page_ai.cancel)}</Button>
              <Button size="sm" onClick={() => void submit()} disabled={state.status === "loading"}>
                {state.status === "loading" && <Loader2 className="size-3.5 animate-spin" />}
                {t(($) => $.page_ai.run)}
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

export { EmptyLineAiMenu };
