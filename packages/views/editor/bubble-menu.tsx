"use client";

/**
 * EditorBubbleMenu — floating formatting toolbar for text selection.
 *
 * Positioned with @floating-ui/dom (computePosition + autoUpdate) and
 * portaled to document.body via createPortal. This escapes ALL overflow
 * containers in the ancestor chain (Card overflow:hidden, scrollable
 * containers, etc.) while autoUpdate monitors every ancestor scroll
 * container to keep the menu anchored to the selection.
 *
 * Key design decisions:
 * - contextElement on the virtual reference tells Floating UI where to
 *   find scroll ancestors, enabling the hide middleware to detect
 *   nested scroll container clipping.
 * - visibility:hidden (not display:none) keeps the element measurable
 *   so computePosition can size it correctly on first show.
 * - onMouseDown preventDefault on the portal root prevents all clicks
 *   inside the menu from stealing focus from the editor.
 */

import { useState, useEffect, useCallback, useRef, useMemo } from "react";
import {
  computePosition,
  offset,
  flip,
  shift,
  hide,
  autoUpdate,
} from "@floating-ui/dom";
import { useEditorState } from "@tiptap/react";
import type { Editor } from "@tiptap/core";
import { posToDOMRect } from "@tiptap/core";
import { Slice } from "@tiptap/pm/model";
import { NodeSelection } from "@tiptap/pm/state";
import { toast } from "sonner";
import type { NoteAIEditResult, NoteAIJobStatus } from "@multica/core/types";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useCreateIssue } from "@multica/core/issues/mutations";
import { useT } from "../i18n";
import { modKey } from "@multica/core/platform";
import { NoteAIDiffPreview } from "./note-ai-diff";
import { Toggle } from "@multica/ui/components/ui/toggle";
import { Separator } from "@multica/ui/components/ui/separator";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  TooltipProvider,
} from "@multica/ui/components/ui/tooltip";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Input } from "@multica/ui/components/ui/input";
import { Button } from "@multica/ui/components/ui/button";
import {
  Bold,
  Italic,
  Strikethrough,
  Code,
  Highlighter,
  Link2,
  List,
  ListOrdered,
  ListTodo,
  Quote,
  ChevronDown,
  Check,
  X,
  Unlink,
  Type,
  Heading1,
  Heading2,
  Heading3,
  FilePlus,
  Loader2,
  Sparkles,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

export type TextOptimizationRequest = {
  selectedText: string;
  instruction: string;
  contextBefore: string;
  contextAfter: string;
};

type TextOptimizationAction = (request: TextOptimizationRequest, options?: { signal?: AbortSignal; onStatus?: (status: NoteAIJobStatus) => void }) => Promise<NoteAIEditResult>;

type TextOptimizationDraft = TextOptimizationRequest & { from: number; to: number };

type TextOptimizationState =
  | { status: "idle" }
  | ({ status: "prompt" } & TextOptimizationDraft)
  | ({ status: "loading"; abort: AbortController; jobStatus?: NoteAIJobStatus; cancelling?: boolean } & TextOptimizationDraft)
  | { status: "review"; from: number; to: number; result: NoteAIEditResult };

const OPTIMIZATION_CONTEXT_CHARS = 1600;

function buildTextOptimizationDraft(editor: Editor): TextOptimizationDraft | null {
  const { from, to } = editor.state.selection;
  if (from === to) return null;
  const selectedText = editor.state.doc.textBetween(from, to, "\n", "\n").trim();
  if (!selectedText) return null;
  const contextFrom = Math.max(0, from - OPTIMIZATION_CONTEXT_CHARS);
  const contextTo = Math.min(editor.state.doc.content.size, to + OPTIMIZATION_CONTEXT_CHARS);
  return {
    from,
    to,
    selectedText,
    instruction: "",
    contextBefore: editor.state.doc.textBetween(contextFrom, from, "\n", "\n").trim(),
    contextAfter: editor.state.doc.textBetween(to, contextTo, "\n", "\n").trim(),
  };
}

function noteAIStatusLabel(status: NoteAIJobStatus | undefined, t: any) {
  switch (status) {
  case "queued":
    return t(($: any) => $.bubble_menu.optimize.status_queued);
  case "dispatched":
    return t(($: any) => $.bubble_menu.optimize.status_dispatched);
  case "running":
    return t(($: any) => $.bubble_menu.optimize.status_running);
  case "completed":
    return t(($: any) => $.bubble_menu.optimize.status_completed);
  case "failed":
    return t(($: any) => $.bubble_menu.optimize.status_failed);
  case "cancelled":
    return t(($: any) => $.bubble_menu.optimize.status_cancelled);
  default:
    return t(($: any) => $.bubble_menu.optimize.status_starting);
  }
}

function shouldShowBubbleMenu(editor: Editor): boolean {
  if (!editor.isEditable) return false;
  const { selection } = editor.state;
  if (selection.empty) return false;
  const { from, to } = selection;
  if (!editor.state.doc.textBetween(from, to).trim().length) return false;
  if (selection instanceof NodeSelection) return false;
  const $from = editor.state.doc.resolve(from);
  if ($from.parent.type.name === "codeBlock") return false;
  return true;
}

// ---------------------------------------------------------------------------
// Mark Toggle Button
// ---------------------------------------------------------------------------

type InlineMark = "bold" | "italic" | "strike" | "code" | "highlight";

const toggleMarkActions: Record<InlineMark, (editor: Editor) => void> = {
  bold: (e) => e.chain().focus().toggleBold().run(),
  italic: (e) => e.chain().focus().toggleItalic().run(),
  strike: (e) => e.chain().focus().toggleStrike().run(),
  code: (e) => e.chain().focus().toggleCode().run(),
  highlight: (e) => e.chain().focus().toggleHighlight().run(),
};

function MarkButton({
  editor,
  mark,
  icon: Icon,
  label,
  shortcut,
  isActive,
}: {
  editor: Editor;
  mark: InlineMark;
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  shortcut: string;
  isActive: boolean;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Toggle
            size="sm"
            pressed={isActive}
            onPressedChange={() => toggleMarkActions[mark](editor)}
            onMouseDown={(e) => e.preventDefault()}
          />
        }
      >
        <Icon className="size-3.5" />
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={8}>
        {label}
        <span className="ml-1.5 text-muted-foreground">{shortcut}</span>
      </TooltipContent>
    </Tooltip>
  );
}

// ---------------------------------------------------------------------------
// Content replacement + URL normalisation
// ---------------------------------------------------------------------------

/** Protocols that can execute code in the browser — the only ones we block. */
const DANGEROUS_PROTOCOL_RE = /^(javascript|data|vbscript):/i;
const HAS_PROTOCOL_RE = /^[a-z][a-z0-9+.-]*:\/?\/?/i;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * Normalise a user-entered URL: add protocol, detect mailto, block XSS.
 *
 * Uses a blocklist (not allowlist) for protocols — only `javascript:`,
 * `data:`, and `vbscript:` are blocked. All other protocols pass through
 * because they can't execute code in the browser and are legitimate
 * deep-link targets in a team tool (slack://, vscode://, figma://).
 * Tiptap's `isAllowedUri` in the `setLink` command provides a second
 * safety layer.
 */
function replaceEditorRangeWithMarkdown(editor: Editor, from: number, to: number, markdown: string) {
  if (!editor.markdown) {
    editor.chain().focus().command(({ tr }) => {
      tr.insertText(markdown, from, to);
      return true;
    }).run();
    return;
  }
  const json = editor.markdown.parse(markdown);
  const node = editor.schema.nodeFromJSON(json);
  const slice = Slice.maxOpen(node.content);
  editor.chain().focus().command(({ tr }) => {
    tr.replaceRange(from, to, slice);
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

function normalizeUrl(input: string): string {
  const trimmed = input.trim();
  if (!trimmed) return "";
  if (trimmed.startsWith("/")) return trimmed;
  if (DANGEROUS_PROTOCOL_RE.test(trimmed)) return "";
  if (HAS_PROTOCOL_RE.test(trimmed)) return trimmed;
  if (EMAIL_RE.test(trimmed)) return `mailto:${trimmed}`;
  if (trimmed.startsWith("//")) return `https:${trimmed}`;
  return `https://${trimmed}`;
}

// ---------------------------------------------------------------------------
// Link Edit Bar
// ---------------------------------------------------------------------------

function LinkEditBar({
  editor,
  onClose,
}: {
  editor: Editor;
  onClose: () => void;
}) {
  const { t } = useT("editor");
  const existingHref = editor.getAttributes("link").href as string | undefined;
  const [url, setUrl] = useState(existingHref ?? "");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const t = setTimeout(() => inputRef.current?.focus(), 0);
    return () => clearTimeout(t);
  }, []);

  const apply = useCallback(() => {
    const href = normalizeUrl(url);
    if (!href) {
      editor.chain().focus().extendMarkRange("link").unsetLink().run();
    } else {
      editor.chain().focus().extendMarkRange("link").setLink({ href }).run();
    }
    onClose();
  }, [editor, url, onClose]);

  const remove = useCallback(() => {
    editor.chain().focus().extendMarkRange("link").unsetLink().run();
    onClose();
  }, [editor, onClose]);

  return (
    <div className="bubble-menu-link-edit" onMouseDown={(e) => e.preventDefault()}>
      <Input
        ref={inputRef}
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder="https://..."
        aria-label={t(($) => $.bubble_menu.url_aria_label)}
        className="h-7 flex-1 text-xs"
        onKeyDown={(e) => {
          if (e.key === "Enter") { e.preventDefault(); apply(); }
          if (e.key === "Escape") { e.preventDefault(); onClose(); editor.commands.focus(); }
        }}
      />
      <Button size="icon-xs" variant="ghost" onClick={apply} onMouseDown={(e) => e.preventDefault()}>
        <Check className="size-3.5" />
      </Button>
      {existingHref && (
        <Button size="icon-xs" variant="ghost" onClick={remove} onMouseDown={(e) => e.preventDefault()}>
          <Unlink className="size-3.5" />
        </Button>
      )}
      <Button size="icon-xs" variant="ghost" onClick={() => { onClose(); editor.commands.focus(); }} onMouseDown={(e) => e.preventDefault()}>
        <X className="size-3.5" />
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Heading Dropdown
// ---------------------------------------------------------------------------

function HeadingDropdown({ editor, onOpenChange, activeLevel }: { editor: Editor; onOpenChange: (open: boolean) => void; activeLevel: number | undefined }) {
  const { t } = useT("editor");
  const [open, setOpen] = useState(false);
  const label = activeLevel ? `H${activeLevel}` : t(($) => $.bubble_menu.heading_dropdown.text);
  const items = [
    { label: t(($) => $.bubble_menu.heading_dropdown.normal_text), icon: Type, active: !activeLevel, action: () => editor.chain().focus().setParagraph().run() },
    { label: t(($) => $.bubble_menu.heading_dropdown.heading_1), icon: Heading1, active: activeLevel === 1, action: () => editor.chain().focus().toggleHeading({ level: 1 }).run() },
    { label: t(($) => $.bubble_menu.heading_dropdown.heading_2), icon: Heading2, active: activeLevel === 2, action: () => editor.chain().focus().toggleHeading({ level: 2 }).run() },
    { label: t(($) => $.bubble_menu.heading_dropdown.heading_3), icon: Heading3, active: activeLevel === 3, action: () => editor.chain().focus().toggleHeading({ level: 3 }).run() },
  ];

  const handleOpenChange = useCallback((next: boolean) => {
    setOpen(next);
    onOpenChange(next);
  }, [onOpenChange]);

  return (
    <Popover modal={false} open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        className="inline-flex h-7 items-center gap-0.5 rounded-md px-1.5 text-xs font-medium hover:bg-muted"
        onMouseDown={(e) => e.preventDefault()}
      >
        {label}
        <ChevronDown className="size-3" />
      </PopoverTrigger>
      <PopoverContent
        side="bottom"
        sideOffset={8}
        align="start"
        className="w-auto min-w-32 p-1"
        initialFocus={false}
        finalFocus={false}
      >
        {items.map((item) => (
          <button
            type="button"
            key={item.label}
            className="flex w-full cursor-default items-center gap-2 rounded-md px-1.5 py-1 text-xs outline-hidden select-none hover:bg-accent hover:text-accent-foreground"
            onMouseDown={(e) => {
              e.preventDefault();
              item.action();
              handleOpenChange(false);
            }}
          >
            <item.icon className="size-3.5" />
            {item.label}
            {item.active && <Check className="ml-auto size-3.5" />}
          </button>
        ))}
      </PopoverContent>
    </Popover>
  );
}

// ---------------------------------------------------------------------------
// List Dropdown
// ---------------------------------------------------------------------------

function ListDropdown({ editor, onOpenChange, isBullet, isOrdered, isTask }: { editor: Editor; onOpenChange: (open: boolean) => void; isBullet: boolean; isOrdered: boolean; isTask: boolean }) {
  const { t } = useT("editor");
  const [open, setOpen] = useState(false);

  const handleOpenChange = useCallback((next: boolean) => {
    setOpen(next);
    onOpenChange(next);
  }, [onOpenChange]);

  return (
    <Popover modal={false} open={open} onOpenChange={handleOpenChange}>
      <Tooltip>
        <TooltipTrigger render={
          <PopoverTrigger className="inline-flex h-7 items-center gap-0.5 rounded-md px-1.5 text-xs font-medium hover:bg-muted aria-pressed:bg-muted" aria-pressed={isBullet || isOrdered || isTask} onMouseDown={(e) => e.preventDefault()} />
        }>
          <List className="size-3.5" />
          <ChevronDown className="size-3" />
        </TooltipTrigger>
        <TooltipContent side="top" sideOffset={8}>{t(($) => $.bubble_menu.list)}</TooltipContent>
      </Tooltip>
      <PopoverContent
        side="bottom"
        sideOffset={8}
        align="start"
        className="w-auto min-w-32 p-1"
        initialFocus={false}
        finalFocus={false}
      >
        <button
          type="button"
          className="flex w-full cursor-default items-center gap-2 rounded-md px-1.5 py-1 text-xs outline-hidden select-none hover:bg-accent hover:text-accent-foreground"
          onMouseDown={(e) => {
            e.preventDefault();
            editor.chain().focus().toggleBulletList().run();
            handleOpenChange(false);
          }}
        >
          <List className="size-3.5" /> {t(($) => $.bubble_menu.list_dropdown.bullet_list)}
          {isBullet && <Check className="ml-auto size-3.5" />}
        </button>
        <button
          type="button"
          className="flex w-full cursor-default items-center gap-2 rounded-md px-1.5 py-1 text-xs outline-hidden select-none hover:bg-accent hover:text-accent-foreground"
          onMouseDown={(e) => {
            e.preventDefault();
            editor.chain().focus().toggleOrderedList().run();
            handleOpenChange(false);
          }}
        >
          <ListOrdered className="size-3.5" /> {t(($) => $.bubble_menu.list_dropdown.ordered_list)}
          {isOrdered && <Check className="ml-auto size-3.5" />}
        </button>
        <button
          type="button"
          className="flex w-full cursor-default items-center gap-2 rounded-md px-1.5 py-1 text-xs outline-hidden select-none hover:bg-accent hover:text-accent-foreground"
          onMouseDown={(e) => {
            e.preventDefault();
            editor.chain().focus().toggleTaskList().run();
            handleOpenChange(false);
          }}
        >
          <ListTodo className="size-3.5" /> {t(($) => $.bubble_menu.list_dropdown.task_list)}
          {isTask && <Check className="ml-auto size-3.5" />}
        </button>
      </PopoverContent>
    </Popover>
  );
}

// ---------------------------------------------------------------------------
// AI Optimize Button
// ---------------------------------------------------------------------------

function OptimizeSelectionButton({
  editor,
  onStateChange,
  pending,
}: {
  editor: Editor;
  onStateChange: (state: TextOptimizationState) => void;
  pending: boolean;
}) {
  const { t } = useT("editor");

  const handleClick = useCallback(() => {
    if (pending) return;
    const draft = buildTextOptimizationDraft(editor);
    if (!draft) return;
    onStateChange({ status: "prompt", ...draft });
  }, [editor, onStateChange, pending]);

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Toggle
            size="sm"
            pressed={false}
            disabled={pending}
            onPressedChange={handleClick}
            onMouseDown={(e) => e.preventDefault()}
          />
        }
      >
        {pending ? <Loader2 className="size-3.5 animate-spin" /> : <Sparkles className="size-3.5" />}
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={8}>{t(($) => $.bubble_menu.optimize.tooltip)}</TooltipContent>
    </Tooltip>
  );
}

function TextOptimizationPrompt({
  state,
  onOptimizeSelection,
  onStateChange,
  onClose,
}: {
  state: Extract<TextOptimizationState, { status: "prompt" }>;
  onOptimizeSelection: TextOptimizationAction;
  onStateChange: (state: TextOptimizationState) => void;
  onClose: () => void;
}) {
  const { t } = useT("editor");
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => textareaRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, []);

  const submit = useCallback(async () => {
    const request: TextOptimizationRequest = {
      selectedText: state.selectedText,
      instruction: state.instruction.trim(),
      contextBefore: state.contextBefore,
      contextAfter: state.contextAfter,
    };
    const abort = new AbortController();
    const loadingState = { status: "loading" as const, from: state.from, to: state.to, ...request, abort, jobStatus: "queued" as const, cancelling: false };
    onStateChange(loadingState);
    try {
      const result = await onOptimizeSelection(request, {
        signal: abort.signal,
        onStatus: (jobStatus) => onStateChange({ ...loadingState, jobStatus }),
      });
      if (!result.markdown.trim()) throw new Error(t(($) => $.bubble_menu.optimize.empty_result));
      onStateChange({ status: "review", from: state.from, to: state.to, result });
    } catch (err) {
      onStateChange({ status: "idle" });
      if (err instanceof Error && err.name === "AbortError") return;
      showErrorToast(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.bubble_menu.optimize.failed),
      );
    }
  }, [onOptimizeSelection, onStateChange, state, t]);

  return (
    <div className="w-[min(420px,calc(100vw-32px))] rounded-xl border bg-popover p-3 text-popover-foreground shadow-lg" onMouseDown={(e) => e.preventDefault()}>
      <div className="mb-2 flex items-center gap-2 text-xs font-medium text-muted-foreground">
        <Sparkles className="size-3.5" />
        {t(($) => $.bubble_menu.optimize.instruction_title)}
      </div>
      <textarea
        ref={textareaRef}
        value={state.instruction}
        onChange={(event) => onStateChange({ ...state, instruction: event.target.value })}
        onMouseDown={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
            event.preventDefault();
            void submit();
          }
          if (event.key === "Escape") {
            event.preventDefault();
            onClose();
          }
        }}
        placeholder={t(($) => $.bubble_menu.optimize.instruction_placeholder)}
        aria-label={t(($) => $.bubble_menu.optimize.instruction_title)}
        className="min-h-24 w-full resize-none rounded-lg border bg-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
      />
      <div className="mt-3 flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onClose} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.cancel)}</Button>
        <Button size="sm" onClick={() => void submit()} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.run)}</Button>
      </div>
    </div>
  );
}

function TextOptimizationLoading({
  state,
  onCancel,
}: {
  state: Extract<TextOptimizationState, { status: "loading" }>;
  onCancel: () => void;
}) {
  const { t } = useT("editor");
  const label = state.cancelling ? t(($: any) => $.bubble_menu.optimize.status_cancelling) : noteAIStatusLabel(state.jobStatus, t);
  return (
    <div className="w-[min(360px,calc(100vw-32px))] rounded-xl border bg-popover p-3 text-popover-foreground shadow-lg" onMouseDown={(e) => e.preventDefault()}>
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" />
        <span className="flex-1 font-medium">{label}</span>
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={state.cancelling} onMouseDown={(e) => e.preventDefault()}>
          {t(($) => $.bubble_menu.optimize.cancel)}
        </Button>
      </div>
    </div>
  );
}

function TextOptimizationReview({
  editor,
  state,
  onApplyTitle,
  onClose,
}: {
  editor: Editor;
  state: Extract<TextOptimizationState, { status: "review" }>;
  onApplyTitle?: (title: string) => void;
  onClose: () => void;
}) {
  const { t } = useT("editor");
  const { result } = state;
  const applyTitle = () => {
    if (result.title) onApplyTitle?.(result.title);
  };
  const replaceSelection = () => {
    try {
      replaceEditorRangeWithMarkdown(editor, state.from, state.to, result.markdown);
    } catch {
      showErrorToast(t(($) => $.bubble_menu.optimize.invalid_markdown));
      return;
    }
    applyTitle();
    onClose();
  };
  const insertAfter = () => {
    try {
      replaceEditorRangeWithMarkdown(editor, state.to, state.to, `\n\n${result.markdown}`);
    } catch {
      showErrorToast(t(($) => $.bubble_menu.optimize.invalid_markdown));
      return;
    }
    applyTitle();
    onClose();
  };
  const replacePage = () => {
    try {
      replaceDocumentWithMarkdown(editor, result.markdown);
    } catch {
      showErrorToast(t(($) => $.bubble_menu.optimize.invalid_markdown));
      return;
    }
    applyTitle();
    onClose();
  };
  const applyPatch = () => {
    const patched = patchedDocumentMarkdown(editor.getMarkdown(), result);
    if (!patched) {
      showErrorToast(t(($) => $.bubble_menu.optimize.patch_target_missing));
      return;
    }
    try {
      replaceDocumentWithMarkdown(editor, patched);
    } catch {
      showErrorToast(t(($) => $.bubble_menu.optimize.invalid_markdown));
      return;
    }
    applyTitle();
    onClose();
  };
  const copyPatch = () => {
    if (typeof navigator === "undefined") return;
    void navigator.clipboard?.writeText(result.target ? `${result.target}\n---\n${result.markdown}` : result.markdown);
  };

  return (
    <div className="w-[min(520px,calc(100vw-32px))] rounded-xl border bg-popover p-3 text-popover-foreground shadow-lg" onMouseDown={(e) => e.preventDefault()}>
      <div className="mb-2 flex items-start gap-2 text-xs text-muted-foreground">
        <Sparkles className="mt-0.5 size-3.5" />
        <div className="min-w-0">
          <div className="font-medium">{t(($) => $.bubble_menu.optimize[`action_${result.action}`])}</div>
          {result.rationale && <div className="mt-0.5 truncate text-[11px] text-muted-foreground/80">{result.rationale}</div>}
        </div>
      </div>
      {result.title && (
        <div className="mb-2 rounded-md bg-muted/60 px-2 py-1.5 text-xs text-muted-foreground">
          {t(($) => $.bubble_menu.optimize.title_suggestion)} <span className="font-medium text-foreground">{result.title}</span>
        </div>
      )}
      {result.action === "patch" && result.target ? (
        <NoteAIDiffPreview
          before={result.target}
          after={result.markdown}
          beforeLabel={t(($) => $.bubble_menu.optimize.current_fragment)}
          afterLabel={t(($) => $.bubble_menu.optimize.replacement_fragment)}
          emptyLabel={t(($) => $.bubble_menu.optimize.no_diff)}
          className="max-h-56"
        />
      ) : result.action === "replace_page" ? (
        <NoteAIDiffPreview
          before={editor.getMarkdown().trimEnd()}
          after={result.markdown}
          beforeLabel={t(($) => $.bubble_menu.optimize.current_page)}
          afterLabel={t(($) => $.bubble_menu.optimize.proposed_page)}
          emptyLabel={t(($) => $.bubble_menu.optimize.no_diff)}
          className="max-h-56"
        />
      ) : (
        <div className="max-h-52 overflow-y-auto whitespace-pre-wrap rounded-lg bg-muted/60 p-3 text-sm leading-6">
          {result.markdown}
        </div>
      )}
      <div className="mt-3 flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onClose} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.discard)}</Button>
        {result.action === "patch" && <Button size="sm" variant="outline" onClick={copyPatch} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.copy_patch)}</Button>}
        {result.action === "insert" ? (
          <Button size="sm" onClick={insertAfter} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.insert_after)}</Button>
        ) : result.action === "replace_page" ? (
          <Button size="sm" onClick={replacePage} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.replace_page)}</Button>
        ) : result.action === "patch" ? (
          <Button size="sm" onClick={applyPatch} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.apply_patch)}</Button>
        ) : (
          <Button size="sm" onClick={replaceSelection} onMouseDown={(e) => e.preventDefault()}>{t(($) => $.bubble_menu.optimize.replace)}</Button>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Create Sub-Issue Button
// ---------------------------------------------------------------------------

/**
 * Turns the current selection into a sub-issue of `parentIssueId` and replaces
 * the selection with a mention link to the new issue. Title is the selected
 * text (trimmed, collapsed whitespace, capped). Only rendered when a parent
 * issue is in scope; otherwise there's no meaningful "sub-issue of" target.
 */
function CreateSubIssueButton({
  editor,
  parentIssueId,
}: {
  editor: Editor;
  parentIssueId: string;
}) {
  const { t } = useT("editor");
  const createIssue = useCreateIssue();
  const [pending, setPending] = useState(false);

  const handleClick = useCallback(async () => {
    if (pending) return;
    const { from, to } = editor.state.selection;
    if (from === to) return;

    // Title from selection: collapse whitespace, cap length. The full selection
    // still becomes the link text — only the issue title is capped.
    const rawTitle = editor.state.doc.textBetween(from, to, " ", " ").trim();
    const title = rawTitle.replace(/\s+/g, " ").slice(0, 200);
    if (!title) return;

    setPending(true);
    try {
      const newIssue = await createIssue.mutateAsync({
        title,
        parent_issue_id: parentIssueId,
      });
      editor
        .chain()
        .focus()
        .insertContentAt(
          { from, to },
          [
            {
              type: "mention",
              attrs: {
                id: newIssue.id,
                label: newIssue.identifier,
                type: "issue",
              },
            },
            { type: "text", text: " " },
          ],
        )
        .run();
      toast.success(t(($) => $.bubble_menu.sub_issue.created, { identifier: newIssue.identifier }));
    } catch (err) {
      showErrorToast(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.bubble_menu.sub_issue.create_failed),
      );
    } finally {
      setPending(false);
    }
  }, [editor, parentIssueId, createIssue, pending, t]);

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Toggle
            size="sm"
            pressed={false}
            disabled={pending}
            onPressedChange={handleClick}
            onMouseDown={(e) => e.preventDefault()}
          />
        }
      >
        {pending ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : (
          <FilePlus className="size-3.5" />
        )}
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={8}>
        {t(($) => $.bubble_menu.sub_issue.tooltip)}
      </TooltipContent>
    </Tooltip>
  );
}

// ---------------------------------------------------------------------------
// Main Bubble Menu — @floating-ui/dom + portal to body
// ---------------------------------------------------------------------------

function EditorBubbleMenu({
  editor,
  currentIssueId,
  onOptimizeSelection,
  onApplyTitle,
}: {
  editor: Editor;
  currentIssueId?: string;
  onOptimizeSelection?: TextOptimizationAction;
  onApplyTitle?: (title: string) => void;
}) {
  const { t } = useT("editor");
  const [visible, setVisible] = useState(false);
  const [mode, setMode] = useState<"toolbar" | "link-edit">("toolbar");
  const [optimizationState, setOptimizationState] = useState<TextOptimizationState>({ status: "idle" });
  const optimizationStateRef = useRef(optimizationState);
  optimizationStateRef.current = optimizationState;
  const resetOptimizationRef = useRef<() => void>(() => {});
  resetOptimizationRef.current = () => {
    const current = optimizationStateRef.current;
    if (current.status === "loading") current.abort.abort();
    setOptimizationState({ status: "idle" });
  };
  const resetOptimization = useCallback(() => resetOptimizationRef.current(), []);
  const floatingRef = useRef<HTMLDivElement>(null);

  // Precise subscription to formatting state — only re-renders when these
  // values actually change, not on every transaction.
  const fmt = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive("bold"),
      italic: e.isActive("italic"),
      strike: e.isActive("strike"),
      code: e.isActive("code"),
      highlight: e.isActive("highlight"),
      link: e.isActive("link"),
      blockquote: e.isActive("blockquote"),
      bulletList: e.isActive("bulletList"),
      orderedList: e.isActive("orderedList"),
      taskList: e.isActive("taskList"),
      heading1: e.isActive("heading", { level: 1 }),
      heading2: e.isActive("heading", { level: 2 }),
      heading3: e.isActive("heading", { level: 3 }),
    }),
  });

  // Virtual reference that tracks the text selection.
  // contextElement tells autoUpdate/hide where to find scroll ancestors.
  const virtualRef = useMemo(
    () => ({
      getBoundingClientRect: () => {
        if (editor.isDestroyed) return new DOMRect();
        const { from, to } = editor.state.selection;
        return posToDOMRect(editor.view, from, to);
      },
      contextElement: editor.view.dom,
    }),
    [editor],
  );

  // Show/hide based on selection state
  useEffect(() => {
    const onTransaction = () => {
      if (!editor.isInitialized) return;
      setVisible(shouldShowBubbleMenu(editor));
    };
    editor.on("transaction", onTransaction);
    return () => { editor.off("transaction", onTransaction); };
  }, [editor]);

  // Hide on blur — debounced to allow focus to settle (e.g. clicking menu)
  useEffect(() => {
    const onBlur = () => {
      setTimeout(() => {
        if (editor.isDestroyed) return;
        const el = floatingRef.current;
        if (el && el.contains(document.activeElement)) return;
        if (editor.view.hasFocus()) return;
        setVisible(false);
      }, 0);
    };
    editor.on("blur", onBlur);
    return () => { editor.off("blur", onBlur); };
  }, [editor]);

  // Position the floating element with autoUpdate when visible
  useEffect(() => {
    const el = floatingRef.current;
    if (!visible || !el || !editor.isInitialized) return;

    const updatePosition = () => {
      computePosition(virtualRef, el, {
        strategy: "fixed",
        placement: "top",
        middleware: [offset(8), flip(), shift({ padding: 8 }), hide()],
      }).then(({ x, y, middlewareData }) => {
        if (!el.isConnected) return;
        const hidden = middlewareData.hide?.referenceHidden;
        el.style.visibility = hidden ? "hidden" : "visible";
        el.style.left = `${x}px`;
        el.style.top = `${y}px`;
      });
    };

    // autoUpdate monitors all scroll ancestors (via contextElement),
    // resize, and animation frames — no manual scroll listener needed.
    const cleanup = autoUpdate(virtualRef, el, updatePosition);
    return cleanup;
  }, [visible, editor, virtualRef]);

  // Close on outside click
  useEffect(() => {
    if (!visible) return;
    const handle = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (editor.view.dom.contains(target)) return;
      if (floatingRef.current?.contains(target)) return;
      resetOptimizationRef.current();
      setVisible(false);
    };
    document.addEventListener("mousedown", handle);
    return () => document.removeEventListener("mousedown", handle);
  }, [visible, editor]);

  // Reset mode on selection change
  useEffect(() => {
    const handler = () => {
      setMode("toolbar");
      resetOptimizationRef.current();
    };
    editor.on("selectionUpdate", handler);
    return () => { editor.off("selectionUpdate", handler); };
  }, [editor]);

  // Refocus editor when Popover closes
  const handleMenuOpenChange = useCallback(
    (open: boolean) => { if (!open) editor.commands.focus(); },
    [editor],
  );

  const cancelOptimization = useCallback(() => {
    const current = optimizationStateRef.current;
    if (current.status !== "loading") return;
    setOptimizationState({ ...current, cancelling: true });
    current.abort.abort();
  }, []);

  return (
    <div
      ref={floatingRef}
      style={{
        position: "fixed",
        zIndex: 50,
        width: "max-content",
        visibility: visible ? "visible" : "hidden",
      }}
      onMouseDown={(e) => e.preventDefault()}
    >
      {optimizationState.status === "prompt" && onOptimizeSelection ? (
        <TextOptimizationPrompt state={optimizationState} onOptimizeSelection={onOptimizeSelection} onStateChange={setOptimizationState} onClose={() => { resetOptimization(); editor.commands.focus(); }} />
      ) : optimizationState.status === "loading" ? (
        <TextOptimizationLoading state={optimizationState} onCancel={cancelOptimization} />
      ) : optimizationState.status === "review" ? (
        <TextOptimizationReview editor={editor} state={optimizationState} onApplyTitle={onApplyTitle} onClose={() => { resetOptimization(); editor.commands.focus(); }} />
      ) : mode === "link-edit" ? (
        <LinkEditBar editor={editor} onClose={() => { setMode("toolbar"); editor.commands.focus(); }} />
      ) : (
        <TooltipProvider delay={300}>
          <div className="bubble-menu">
            <MarkButton editor={editor} mark="bold" icon={Bold} label={t(($) => $.bubble_menu.bold)} shortcut={`${modKey}+B`} isActive={fmt.bold} />
            <MarkButton editor={editor} mark="italic" icon={Italic} label={t(($) => $.bubble_menu.italic)} shortcut={`${modKey}+I`} isActive={fmt.italic} />
            <MarkButton editor={editor} mark="strike" icon={Strikethrough} label={t(($) => $.bubble_menu.strikethrough)} shortcut={`${modKey}+Shift+S`} isActive={fmt.strike} />
            <MarkButton editor={editor} mark="code" icon={Code} label={t(($) => $.bubble_menu.code)} shortcut={`${modKey}+E`} isActive={fmt.code} />
            <MarkButton editor={editor} mark="highlight" icon={Highlighter} label={t(($) => $.bubble_menu.highlight)} shortcut={`${modKey}+Shift+H`} isActive={fmt.highlight} />
            <Separator orientation="vertical" className="mx-0.5 h-5" />
            <Tooltip>
              <TooltipTrigger render={
                <Toggle size="sm" pressed={fmt.link} onPressedChange={() => setMode("link-edit")} onMouseDown={(e) => e.preventDefault()} />
              }>
                <Link2 className="size-3.5" />
              </TooltipTrigger>
              <TooltipContent side="top" sideOffset={8}>{t(($) => $.bubble_menu.link)}</TooltipContent>
            </Tooltip>
            <Separator orientation="vertical" className="mx-0.5 h-5" />
            <HeadingDropdown editor={editor} onOpenChange={handleMenuOpenChange} activeLevel={fmt.heading1 ? 1 : fmt.heading2 ? 2 : fmt.heading3 ? 3 : undefined} />
            <ListDropdown editor={editor} onOpenChange={handleMenuOpenChange} isBullet={fmt.bulletList} isOrdered={fmt.orderedList} isTask={fmt.taskList} />
            <Tooltip>
              <TooltipTrigger render={
                <Toggle size="sm" pressed={fmt.blockquote} onPressedChange={() => editor.chain().focus().toggleBlockquote().run()} onMouseDown={(e) => e.preventDefault()} />
              }>
                <Quote className="size-3.5" />
              </TooltipTrigger>
              <TooltipContent side="top" sideOffset={8}>{t(($) => $.bubble_menu.quote)}</TooltipContent>
            </Tooltip>
            {onOptimizeSelection && (
              <OptimizeSelectionButton editor={editor} onStateChange={setOptimizationState} pending={false} />
            )}
            {currentIssueId && (
              <>
                <Separator orientation="vertical" className="mx-0.5 h-5" />
                <CreateSubIssueButton editor={editor} parentIssueId={currentIssueId} />
              </>
            )}
          </div>
        </TooltipProvider>
      )}
    </div>
  );
}

export { EditorBubbleMenu };
