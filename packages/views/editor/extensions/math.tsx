"use client";

import { useEffect, useRef, useState } from "react";
import { Node, mergeAttributes, nodeInputRule } from "@tiptap/core";
import { ReactNodeViewRenderer, NodeViewWrapper } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";
import { cn } from "@multica/ui/lib/utils";

/**
 * LRM-1264 R2 — KaTeX JS (+ CSS) loads on first math node paint, not with the
 * editor module graph. Composer open without math never pays for KaTeX.
 */

const DEFAULT_INLINE_EXPRESSION = "x^2";
const DEFAULT_BLOCK_EXPRESSION = "E = mc^2";

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    inlineMath: {
      setInlineMath: (expression: string) => ReturnType;
    };
    blockMath: {
      setBlockMath: (expression?: string) => ReturnType;
    };
  }
}

function useKatexHtml(expression: string, displayMode: boolean): string {
  const [rendered, setRendered] = useState({ expression: "", displayMode, html: "" });
  useEffect(() => {
    let alive = true;
    if (expression.trim()) {
      void Promise.all([
        import("katex"),
        import("@multica/ui/markdown/katex-style"),
      ]).then(([katexMod]) => {
        if (!alive) return;
        setRendered({
          expression,
          displayMode,
          html: katexMod.default.renderToString(expression, {
            displayMode,
            output: "htmlAndMathml",
            strict: "warn",
            throwOnError: false,
          }),
        });
      });
    }
    return () => {
      alive = false;
    };
  }, [expression, displayMode]);
  if (!expression.trim()) return "";
  return rendered.expression === expression && rendered.displayMode === displayMode
    ? rendered.html
    : "";
}

function focusAfterNode({ editor, getPos, node }: Pick<NodeViewProps, "editor" | "getPos" | "node">) {
  if (typeof getPos !== "function") {
    editor.commands.focus();
    return;
  }
  const pos = getPos();
  if (typeof pos !== "number") {
    editor.commands.focus();
    return;
  }
  editor.chain().focus().setTextSelection(pos + node.nodeSize).run();
}

export function InlineMathView({ node, editor, getPos, updateAttributes }: NodeViewProps) {
  const expression = String(node.attrs.expression ?? "");
  const [editing, setEditing] = useState(expression.trim() === "");
  const inputRef = useRef<HTMLInputElement | null>(null);
  const html = useKatexHtml(expression, false);

  useEffect(() => {
    if (!editing) return;
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [editing]);

  const close = () => {
    setEditing(false);
    focusAfterNode({ editor, getPos, node });
  };

  return (
    <NodeViewWrapper
      as="span"
      className={cn("math-node inline", editing && "editing")}
      data-type="inline-math"
      data-expression={expression}
      contentEditable={false}
      onClick={() => setEditing(true)}
    >
      {html ? (
        <span dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <span>{`$${expression || DEFAULT_INLINE_EXPRESSION}$`}</span>
      )}
      {editing && (
        <span className="math-node-popover" onClick={(event) => event.stopPropagation()}>
          <input
            ref={inputRef}
            value={expression}
            onChange={(event) => updateAttributes({ expression: event.target.value })}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === "Escape") {
                event.preventDefault();
                close();
              }
            }}
            onBlur={() => setEditing(false)}
            className="math-node-input"
            placeholder="x^2"
            aria-label="Edit inline formula"
          />
        </span>
      )}
    </NodeViewWrapper>
  );
}

export function BlockMathView({ node, editor, getPos, updateAttributes }: NodeViewProps) {
  const expression = String(node.attrs.expression ?? "");
  const [editing, setEditing] = useState(expression.trim() === "");
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const html = useKatexHtml(expression, true);

  useEffect(() => {
    if (!editing) return;
    const timer = window.setTimeout(() => textareaRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [editing]);

  const close = () => {
    setEditing(false);
    focusAfterNode({ editor, getPos, node });
  };

  return (
    <NodeViewWrapper
      as="div"
      className={cn("math-node block", editing && "editing")}
      data-type="block-math"
      data-expression={expression}
      contentEditable={false}
      onClick={() => setEditing(true)}
    >
      {editing ? (
        <div className="math-node-editor" onClick={(event) => event.stopPropagation()}>
          <div className="math-node-preview" aria-hidden="true">
            {html ? (
              <div dangerouslySetInnerHTML={{ __html: html }} />
            ) : (
              <span>{expression || DEFAULT_BLOCK_EXPRESSION}</span>
            )}
          </div>
          <textarea
            ref={textareaRef}
            value={expression}
            onChange={(event) => updateAttributes({ expression: event.target.value })}
            onKeyDown={(event) => {
              if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
                event.preventDefault();
                close();
              }
              if (event.key === "Escape") {
                event.preventDefault();
                close();
              }
            }}
            onBlur={() => setEditing(false)}
            className="math-node-textarea"
            placeholder="E = mc^2"
            aria-label="Edit block formula"
          />
        </div>
      ) : html ? (
        <div dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <div>{expression || DEFAULT_BLOCK_EXPRESSION}</div>
      )}
    </NodeViewWrapper>
  );
}

// react-doctor-disable-next-line react-doctor/only-export-components -- Tiptap extension exports stay with their NodeViews for ReactNodeViewRenderer.
export const InlineMathExtension = Node.create({
  name: "inlineMath",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      expression: {
        default: "",
        rendered: false,
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: 'span[data-type="inline-math"]',
        getAttrs: (el) => ({
          expression: (el as HTMLElement).getAttribute("data-expression") ?? "",
        }),
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        "data-type": "inline-math",
        "data-expression": node.attrs.expression,
      }),
      node.attrs.expression,
    ];
  },

  markdownTokenizer: {
    name: "inlineMath",
    level: "inline" as const,
    start(src: string) {
      return src.indexOf("$");
    },
    tokenize(src: string) {
      if (!src.startsWith("$") || src.startsWith("$$")) return undefined;
      const match = src.match(/^\$((?:\\.|[^$\\\n])+?)\$/);
      if (!match) return undefined;
      return {
        type: "inlineMath",
        raw: match[0],
        attributes: { expression: match[1] },
      };
    },
  },

  parseMarkdown: (token: any, helpers: any) => {
    return helpers.createNode("inlineMath", token.attributes);
  },

  renderMarkdown: (node: any) => {
    const expression = String(node.attrs?.expression ?? "");
    return `$${expression}$`;
  },

  addCommands() {
    return {
      setInlineMath:
        (expression: string) =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            attrs: { expression: expression || DEFAULT_INLINE_EXPRESSION },
          }),
    };
  },

  addKeyboardShortcuts() {
    return {
      "Mod-Shift-e": () => {
        const { from, to, empty } = this.editor.state.selection;
        const selected = empty
          ? DEFAULT_INLINE_EXPRESSION
          : this.editor.state.doc.textBetween(from, to, " ", " ").trim();
        if (!empty) this.editor.commands.deleteRange({ from, to });
        return this.editor.commands.setInlineMath(selected || DEFAULT_INLINE_EXPRESSION);
      },
    };
  },

  addInputRules() {
    return [
      nodeInputRule({
        find: /\$(?:\\.|[^$\\\n])+\$$/,
        type: this.type,
        getAttributes: (match) => ({
          expression: match[0].slice(1, -1),
        }),
      }),
    ];
  },

  addNodeView() {
    return ReactNodeViewRenderer(InlineMathView);
  },
});

// react-doctor-disable-next-line react-doctor/only-export-components -- Tiptap extension exports stay with their NodeViews for ReactNodeViewRenderer.
export const BlockMathExtension = Node.create({
  name: "blockMath",
  group: "block",
  atom: true,
  code: true,
  defining: true,
  isolating: true,
  selectable: true,

  addAttributes() {
    return {
      expression: {
        default: "",
        rendered: false,
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: 'div[data-type="block-math"]',
        getAttrs: (el) => ({
          expression: (el as HTMLElement).getAttribute("data-expression") ?? "",
        }),
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "div",
      mergeAttributes(HTMLAttributes, {
        "data-type": "block-math",
        "data-expression": node.attrs.expression,
      }),
      node.attrs.expression,
    ];
  },

  markdownTokenizer: {
    name: "blockMath",
    level: "block" as const,
    start(src: string) {
      return src.search(/^\$\$/m);
    },
    tokenize(src: string) {
      if (!src.startsWith("$$")) return undefined;
      const match = src.match(/^\$\$[ \t]*\n?([\s\S]+?)\n?\$\$(?:\n|$)/);
      if (!match) return undefined;
      return {
        type: "blockMath",
        raw: match[0],
        attributes: { expression: match[1] ?? "" },
      };
    },
  },

  parseMarkdown: (token: any, helpers: any) => {
    return helpers.createNode("blockMath", token.attributes);
  },

  renderMarkdown: (node: any) => {
    const expression = String(node.attrs?.expression ?? "");
    return `$$\n${expression}\n$$`;
  },

  addCommands() {
    return {
      setBlockMath:
        (expression = "") =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            attrs: { expression },
          }),
    };
  },

  addNodeView() {
    return ReactNodeViewRenderer(BlockMathView);
  },
});
