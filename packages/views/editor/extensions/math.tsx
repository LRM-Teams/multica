"use client";

import { useEffect, useState } from "react";
import { Node, mergeAttributes, nodeInputRule } from "@tiptap/core";
import { ReactNodeViewRenderer, NodeViewWrapper } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";

/**
 * LRM-1264 R2 — KaTeX JS (+ CSS) loads on first math node paint, not with the
 * editor module graph. Composer open without math never pays for KaTeX.
 */

function useKatexHtml(expression: string, displayMode: boolean): string {
  const [html, setHtml] = useState("");
  useEffect(() => {
    let alive = true;
    void Promise.all([
      import("katex"),
      import("@multica/ui/markdown/katex-style"),
    ]).then(([katexMod]) => {
      if (!alive) return;
      setHtml(
        katexMod.default.renderToString(expression, {
          displayMode,
          output: "htmlAndMathml",
          strict: "warn",
          throwOnError: false,
        }),
      );
    });
    return () => {
      alive = false;
    };
  }, [expression, displayMode]);
  return html;
}

function InlineMathView({ node }: NodeViewProps) {
  const expression = String(node.attrs.expression ?? "");
  const html = useKatexHtml(expression, false);
  return (
    <NodeViewWrapper
      as="span"
      className="math-node inline"
      data-type="inline-math"
      data-expression={expression}
      contentEditable={false}
    >
      {html ? (
        <span dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <span>{`$${expression}$`}</span>
      )}
    </NodeViewWrapper>
  );
}

function BlockMathView({ node }: NodeViewProps) {
  const expression = String(node.attrs.expression ?? "");
  const html = useKatexHtml(expression, true);
  return (
    <NodeViewWrapper
      as="div"
      className="math-node block"
      data-type="block-math"
      data-expression={expression}
      contentEditable={false}
    >
      {html ? (
        <div dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <div>{expression}</div>
      )}
    </NodeViewWrapper>
  );
}

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

  addNodeView() {
    return ReactNodeViewRenderer(BlockMathView);
  },
});
