import { Node, mergeAttributes } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { RunReferenceView } from "./run-reference-view";

export const RunReferenceExtension = Node.create({
  name: "runReference",
  group: "inline",
  inline: true,
  atom: true,
  selectable: false,

  addAttributes() {
    return {
      id: { default: null },
      label: { default: null },
      agentId: { default: null },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[data-run-reference-id]",
        getAttrs: (el) => {
          if (!(el instanceof HTMLElement)) return false;
          return {
            id: el.getAttribute("data-run-reference-id"),
            label: el.getAttribute("data-run-reference-label"),
            agentId: el.getAttribute("data-run-reference-agent-id"),
          };
        },
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        "data-run-reference-id": node.attrs.id,
        "data-run-reference-label": node.attrs.label,
        "data-run-reference-agent-id": node.attrs.agentId,
      }),
      node.attrs.label ?? "run",
    ];
  },

  addNodeView() {
    return ReactNodeViewRenderer(RunReferenceView);
  },

  markdownTokenizer: {
    name: "runReference",
    level: "inline" as const,
    start(src: string) {
      return src.search(/\[(?:\\.|[^\]])+\]\(mention:\/\/run\//);
    },
    tokenize(src: string) {
      const match = src.match(/^\[((?:\\.|[^\]])+)\]\(mention:\/\/run\/([^)]+)\)/);
      if (!match) return undefined;
      const rawLabel = match[1]?.replace(/\\\[/g, "[").replace(/\\\]/g, "]");
      return {
        type: "runReference",
        raw: match[0],
        attributes: { label: rawLabel, id: match[2], agentId: null },
      };
    },
  },

  parseMarkdown: (token: any, helpers: any) => {
    return helpers.createNode("runReference", token.attributes);
  },

  renderMarkdown: (node: any) => {
    const { id, label } = node.attrs || {};
    const safeLabel = (label ?? "run").replace(/\[/g, "\\[").replace(/\]/g, "\\]");
    return `[${safeLabel}](mention://run/${id})`;
  },
});
