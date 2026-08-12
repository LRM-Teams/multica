import { Node, mergeAttributes } from "@tiptap/core";
import { NodeViewWrapper, ReactNodeViewRenderer } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";
import { appendQueryParams, useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";
import { cn } from "@multica/ui/lib/utils";

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

function RunReferenceView({ node }: NodeViewProps) {
  const { id, label, agentId } = node.attrs;
  const { t } = useT("editor");
  const paths = useWorkspacePaths();
  const { push, openInNewTab } = useNavigation();
  const href = agentId
    ? appendQueryParams(paths.agentDetail(agentId), { run: id })
    : paths.agents();

  return (
    <NodeViewWrapper as="span" className="inline">
      <button
        type="button"
        className={cn(
          "mention inline-flex items-center rounded-md px-1.5 py-0.5 text-[0.9em] font-medium",
          "bg-muted text-foreground hover:bg-muted/80",
        )}
        title={t(($) => $.run_reference.open)}
        onClick={(event) => {
          if (event.metaKey || event.ctrlKey) openInNewTab(href);
          else push(href);
        }}
      >
        {label || t(($) => $.run_reference.fallback_label)}
      </button>
    </NodeViewWrapper>
  );
}
