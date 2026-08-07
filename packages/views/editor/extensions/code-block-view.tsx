"use client";

import { useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { NodeViewWrapper, NodeViewContent } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";
import { NodeSelection } from "@tiptap/pm/state";
import {
  Check,
  ChevronDown,
  Code as CodeIcon,
  Copy,
  GripVertical,
  Download,
  Eye,
  MoreHorizontal,
  SquareSplitVertical,
  ZoomIn,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";
import {
  INSERTABLE_CODE_BLOCK_LANGUAGES,
  setLastInsertedCodeBlockLanguage,
  type InsertableCodeBlockLanguage,
} from "../code-block-language";
import {
  MermaidDiagram,
  type MermaidDiagramHandle,
} from "../mermaid-diagram";
import { CodeBlockIframe } from "../code-block-iframe";
import {
  normalizeMermaidView,
  parseCodeFenceInfo,
  type MermaidViewMode,
} from "./code-block-fence";

// Coalesces fast keystrokes before re-rendering live previews.
// `mermaid.initialize()` mutates a process-global config, so back-to-back
// renders during typing can race a concurrent ReadonlyContent render
// (e.g. a comment card) and clobber its theme variables. 200ms keeps the
// "live preview" feel while making concurrent inits unlikely in practice.
// HTML preview reuses the same debounce: re-keying iframe.srcDoc on every
// keystroke causes the iframe to re-load and flicker.
const PREVIEW_DEBOUNCE_MS = 200;

const HTML_PREVIEW_HEIGHT = "h-[480px]";

type CodeLanguage = InsertableCodeBlockLanguage | "markdown" | "html";

const LANGUAGE_LABELS: Record<CodeLanguage, string> = {
  plaintext: "Plaintext",
  markdown: "Markdown",
  python: "Python",
  javascript: "JavaScript",
  html: "HTML",
  mermaid: "Mermaid",
};

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}

function normalizeLanguage(language: string): CodeLanguage {
  const parsed = parseCodeFenceInfo(language).language;
  if (parsed === "markdown" || parsed === "html" || (INSERTABLE_CODE_BLOCK_LANGUAGES as readonly string[]).includes(parsed)) {
    return parsed as CodeLanguage;
  }
  return "plaintext";
}

function languageLabel(language: string): string {
  return LANGUAGE_LABELS[normalizeLanguage(language)];
}

/** Stop ProseMirror from treating toolbar clicks as editor selection. */
function stopToolbarBubble(event: ReactMouseEvent) {
  event.stopPropagation();
}

/** Keep the editor from stealing focus when choosing a menu item. */
function preserveMenuFocus(event: ReactMouseEvent) {
  event.preventDefault();
  event.stopPropagation();
}

const iconButtonClass =
  "flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground";

interface CodeBlockToolbarProps {
  language: string;
  isMermaid: boolean;
  isHtml: boolean;
  htmlView: "preview" | "source";
  mermaidView: MermaidViewMode;
  copied: boolean;
  mermaidActionsEnabled: boolean;
  onLanguageChange: (language: CodeLanguage) => void;
  onMermaidViewChange: (mode: MermaidViewMode) => void;
  onToggleHtmlView: () => void;
  onCopy: () => void;
  onZoom: () => void;
  onDownload: () => void;
  onDelete: () => void;
  onMenuOpenChange: (open: boolean) => void;
}

function CodeBlockToolbar({
  language,
  isMermaid,
  isHtml,
  htmlView,
  mermaidView,
  copied,
  mermaidActionsEnabled,
  onLanguageChange,
  onMermaidViewChange,
  onToggleHtmlView,
  onCopy,
  onZoom,
  onDownload,
  onDelete,
  onMenuOpenChange,
}: CodeBlockToolbarProps) {
  const { t } = useT("editor");
  const currentLanguage = normalizeLanguage(language || "plaintext");

  return (
    /* react-doctor-disable-next-line react-doctor/no-static-element-interactions -- mousedown stopPropagation only; keeps TipTap from stealing focus. Real actions are the buttons inside. */
    <div
      data-testid="code-block-toolbar"
      className="code-block-toolbar flex items-center gap-0.5 rounded-lg border border-border bg-background p-0.5 shadow-sm"
      onMouseDown={stopToolbarBubble}
    >
      <DropdownMenu onOpenChange={onMenuOpenChange}>
        <DropdownMenuTrigger
          render={
            <button
              type="button"
              data-testid="code-block-language"
              aria-label={t(($) => $.code_block.language)}
              className="flex h-6 items-center gap-1 rounded-md bg-muted px-2 text-xs text-muted-foreground transition-colors hover:text-foreground"
            />
          }
        >
          <span className="select-none">{languageLabel(currentLanguage)}</span>
          <ChevronDown className="h-3 w-3 opacity-70" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" onMouseDown={preserveMenuFocus}>
          {INSERTABLE_CODE_BLOCK_LANGUAGES.map((item) => (
            <DropdownMenuItem
              key={item}
              onClick={() => onLanguageChange(item)}
            >
              {item === currentLanguage ? "✓ " : ""}
              {LANGUAGE_LABELS[item]}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>

      <div className="mx-0.5 h-4 w-px bg-border" aria-hidden />

      {isMermaid && (
        <DropdownMenu onOpenChange={onMenuOpenChange}>
          <DropdownMenuTrigger
            render={
              <button
                type="button"
                data-testid="code-block-mermaid-view"
                className={iconButtonClass}
                title={t(($) => $.code_block.mermaid_view)}
                aria-label={t(($) => $.code_block.mermaid_view)}
              />
            }
          >
            <SquareSplitVertical className="h-3.5 w-3.5" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onMouseDown={preserveMenuFocus}>
            <DropdownMenuItem onClick={() => onMermaidViewChange("source")}>
              {mermaidView === "source" ? "✓ " : ""}
              {t(($) => $.code_block.mermaid_source)}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onMermaidViewChange("diagram")}>
              {mermaidView === "diagram" ? "✓ " : ""}
              {t(($) => $.code_block.mermaid_diagram)}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onMermaidViewChange("both")}>
              {mermaidView === "both" ? "✓ " : ""}
              {t(($) => $.code_block.mermaid_both)}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}

      {isMermaid && (
        <button
          type="button"
          data-testid="code-block-mermaid-zoom"
          onClick={onZoom}
          disabled={!mermaidActionsEnabled}
          className={cn(iconButtonClass, "disabled:pointer-events-none disabled:opacity-40")}
          title={t(($) => $.code_block.fullscreen)}
          aria-label={t(($) => $.code_block.fullscreen)}
        >
          <ZoomIn className="h-3.5 w-3.5" />
        </button>
      )}

      {isMermaid && (
        <button
          type="button"
          data-testid="code-block-mermaid-download"
          onClick={onDownload}
          disabled={!mermaidActionsEnabled}
          className={cn(iconButtonClass, "disabled:pointer-events-none disabled:opacity-40")}
          title={t(($) => $.code_block.download_diagram)}
          aria-label={t(($) => $.code_block.download_diagram)}
        >
          <Download className="h-3.5 w-3.5" />
        </button>
      )}

      {isHtml && (
        <button
          type="button"
          onClick={onToggleHtmlView}
          className={iconButtonClass}
          title={
            htmlView === "preview"
              ? t(($) => $.code_block.show_source)
              : t(($) => $.code_block.show_preview)
          }
          aria-label={
            htmlView === "preview"
              ? t(($) => $.code_block.show_source)
              : t(($) => $.code_block.show_preview)
          }
        >
          {htmlView === "preview" ? (
            <CodeIcon className="h-3.5 w-3.5" />
          ) : (
            <Eye className="h-3.5 w-3.5" />
          )}
        </button>
      )}

      <button
        type="button"
        data-testid="code-block-copy"
        onClick={onCopy}
        className={iconButtonClass}
        title={t(($) => $.code_block.copy_code)}
        aria-label={t(($) => $.code_block.copy_code)}
      >
        {copied ? (
          <Check className="h-3.5 w-3.5" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </button>

      <DropdownMenu onOpenChange={onMenuOpenChange}>
        <DropdownMenuTrigger
          render={
            <button
              type="button"
              data-testid="code-block-more"
              aria-label={t(($) => $.code_block.menu)}
              className={iconButtonClass}
            />
          }
        >
          <MoreHorizontal className="h-3.5 w-3.5" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" onMouseDown={preserveMenuFocus}>
          <DropdownMenuItem variant="destructive" onClick={onDelete}>
            {t(($) => $.code_block.delete)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function CodeBlockView({ node, updateAttributes, deleteNode, editor, getPos }: NodeViewProps) {
  const { t } = useT("editor");
  const [copied, setCopied] = useState(false);
  // HTML blocks default to "preview"; the user can flip to "source" to
  // edit the markup directly. Note: the source `<pre>` MUST stay mounted
  // (just hidden) so ProseMirror keeps its NodeView bindings — unmounting
  // it would break editing.
  const [view, setView] = useState<"preview" | "source">("preview");
  const [toolbarPinned, setToolbarPinned] = useState(false);
  const mermaidRef = useRef<MermaidDiagramHandle>(null);
  const fence = parseCodeFenceInfo(node.attrs.language || "");
  const language = fence.language;
  const isMermaid = language === "mermaid";
  const isHtml = language === "html";
  // Prefer the dedicated attr (updated live). Fall back to a view encoded in
  // the fence info string for content that still carries `mermaid view=…`
  // as the language token from an older parse path.
  const mermaidView = isMermaid
    ? normalizeMermaidView(
        node.attrs.mermaidView != null && node.attrs.mermaidView !== "both"
          ? node.attrs.mermaidView
          : fence.mermaidView,
      )
    : "both";
  const chart = node.textContent;
  const debouncedChart = useDebouncedValue(
    isMermaid ? chart : "",
    PREVIEW_DEBOUNCE_MS,
  );
  const debouncedHtml = useDebouncedValue(
    isHtml ? chart : "",
    PREVIEW_DEBOUNCE_MS,
  );

  const handleCopy = async () => {
    const text = node.textContent;
    if (!text) return;
    if (await copyText(text)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const showHtmlPreview = isHtml && view === "preview";
  const hasMermaidChart = Boolean(debouncedChart.trim());
  const showMermaidDiagram = isMermaid && mermaidView !== "source" && hasMermaidChart;
  // Empty diagram-only Mermaid fences keep the source editor visible so the
  // block still has a normal one-line editing footprint instead of collapsing.
  const showMermaidSource = !isMermaid || mermaidView !== "diagram" || !hasMermaidChart;
  const hideSource = showHtmlPreview || !showMermaidSource;
  const mermaidActionsEnabled = Boolean(showMermaidDiagram);
  const toggleView = () =>
    setView((v) => (v === "preview" ? "source" : "preview"));
  const setLanguage = (nextLanguage: CodeLanguage) => {
    const languageToUse = setLastInsertedCodeBlockLanguage(nextLanguage);
    updateAttributes({
      language: languageToUse,
      mermaidView: languageToUse === "mermaid" ? "both" : "source",
    });
  };
  const setMermaidView = (mode: MermaidViewMode) => {
    // Keep `language` as the bare token so lowlight / ReadonlyContent see
    // `mermaid`, while the view mode rides in attrs + the fence info string.
    updateAttributes({ language: "mermaid", mermaidView: mode });
  };

  const selectCodeBlock = () => {
    if (!editor || typeof getPos !== "function") return;
    const pos = getPos();
    if (typeof pos !== "number") return;
    const tr = editor.state.tr.setSelection(NodeSelection.create(editor.state.doc, pos));
    editor.view.dispatch(tr);
    editor.view.focus();
  };

  return (
    <NodeViewWrapper className="code-block-wrapper group/code relative my-2">
      <div
        className={cn(
          "code-block-frame relative overflow-hidden rounded-md bg-muted",
          isMermaid && mermaidView === "diagram" && !hasMermaidChart && "code-block-frame-mermaid-diagram-only",
        )}
      >
        <button
          type="button"
          data-testid="code-block-select"
          className="absolute left-2 top-2 z-10 flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground group-hover/code:opacity-100 focus-visible:opacity-100"
          onMouseDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
            selectCodeBlock();
          }}
          aria-label={t(($) => $.code_block.select_block)}
          title={t(($) => $.code_block.select_block)}
        >
          <GripVertical className="h-3.5 w-3.5" />
        </button>
        {showMermaidDiagram && (
          <div
            contentEditable={false}
            className="mermaid-diagram-preview p-3"
          >
            <MermaidDiagram
              ref={mermaidRef}
              chart={debouncedChart}
              showToolbar={false}
            />
          </div>
        )}
        {isHtml && showHtmlPreview && (
          // CSS-hidden when toggled off so the `<pre>` below stays mounted —
          // unmounting either side would either lose ProseMirror bindings
          // (source) or thrash iframe.srcDoc (preview).
          <div contentEditable={false} className="p-3">
            <CodeBlockIframe
              html={debouncedHtml}
              title="HTML preview"
              heightClassName={HTML_PREVIEW_HEIGHT}
            />
          </div>
        )}
        <div
          contentEditable={false}
          className={cn(
            "absolute top-2 right-2 z-10 opacity-0 transition-opacity group-hover/code:opacity-100 focus-within:opacity-100",
            toolbarPinned && "opacity-100",
          )}
        >
          <CodeBlockToolbar
            language={language}
            isMermaid={isMermaid}
            isHtml={isHtml}
            htmlView={view}
            mermaidView={mermaidView}
            copied={copied}
            mermaidActionsEnabled={mermaidActionsEnabled}
            onLanguageChange={setLanguage}
            onMermaidViewChange={setMermaidView}
            onToggleHtmlView={toggleView}
            onCopy={() => void handleCopy()}
            onZoom={() => mermaidRef.current?.openFullscreen()}
            onDownload={() => mermaidRef.current?.downloadSvg()}
            onDelete={() => deleteNode()}
            onMenuOpenChange={setToolbarPinned}
          />
        </div>
        {/* `<pre>` + NodeViewContent must remain mounted so the user can keep
            editing the code block contents. Preview-only modes visually hide
            it while ProseMirror still tracks it. */}
        <pre
          spellCheck={false}
          className={cn(
            "code-block-source",
            hideSource && "code-block-source-visually-hidden",
            (showMermaidDiagram || showHtmlPreview) && "code-block-source-attached",
          )}
          aria-hidden={hideSource ? "true" : undefined}
        >
          {/* @ts-expect-error -- NodeViewContent supports as="code" at runtime */}
          <NodeViewContent as="code" />
        </pre>
      </div>
    </NodeViewWrapper>
  );
}

export { CodeBlockView };
// react-doctor-disable-next-line react-doctor/only-export-components -- toolbar + label helpers are unit-tested beside the NodeView that owns them.
export { CodeBlockToolbar, languageLabel };
export type { CodeLanguage };
export type { MermaidViewMode } from "./code-block-fence";
