import { Node, mergeAttributes } from "@tiptap/core";
import { NodeViewWrapper, ReactNodeViewRenderer } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";
import { Suggestion, type SuggestionOptions } from "@tiptap/suggestion";
import { PluginKey } from "@tiptap/pm/state";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { IssueChip } from "../../issues/components/issue-chip";
import { useT } from "../../i18n/use-t";
import type { MentionItem } from "./mention-suggestion";

export const IssueReferenceExtension = Node.create({
  name: "issueReference",
  group: "inline",
  inline: true,
  atom: true,
  selectable: false,

  addOptions() {
    return {
      // Explicit pluginKey: @tiptap/suggestion's Suggestion() falls back to a
      // MODULE-LEVEL SHARED PluginKey("suggestion") when none is given, so
      // any two Suggestion-backed nodes both left at their bare/disabled
      // default collide with "Adding different instances of a keyed plugin"
      // the moment both are registered in the same editor.
      suggestion: {
        char: "#",
        allow: () => false,
        pluginKey: new PluginKey("issueReferenceSuggestionDisabled"),
      } as Omit<SuggestionOptions<MentionItem>, "editor">,
    };
  },

  addProseMirrorPlugins() {
    return [
      Suggestion<MentionItem, MentionItem>({
        editor: this.editor,
        ...this.options.suggestion,
      }),
    ];
  },

  addAttributes() {
    return {
      id: { default: null },
      label: { default: null },
      // Kept for round-trip of older docs; never used for inaccessible display.
      title: { default: null },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[data-issue-reference-id]",
        getAttrs: (el) => {
          if (!(el instanceof HTMLElement)) return false;
          return {
            id: el.getAttribute("data-issue-reference-id"),
            label: el.getAttribute("data-issue-reference-label"),
            title: el.getAttribute("data-issue-reference-title"),
          };
        },
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        "data-issue-reference-id": node.attrs.id,
        "data-issue-reference-label": node.attrs.label,
        "data-issue-reference-title": node.attrs.title,
      }),
      node.attrs.label ?? node.attrs.id,
    ];
  },

  addNodeView() {
    return ReactNodeViewRenderer(IssueReferenceView);
  },

  markdownTokenizer: {
    name: "issueReference",
    level: "inline" as const,
    start(src: string) {
      return src.search(/\[(?:\\.|[^\]])+\]\(mention:\/\/issue\//);
    },
    tokenize(src: string) {
      const match = src.match(/^\[((?:\\.|[^\]])+)\]\(mention:\/\/issue\/([^)]+)\)/);
      if (!match) return undefined;
      const rawLabel = match[1]?.replace(/\\\[/g, "[").replace(/\\\]/g, "]");
      return {
        type: "issueReference",
        raw: match[0],
        attributes: { label: rawLabel, id: match[2] },
      };
    },
  },

  parseMarkdown: (token: any, helpers: any) => {
    return helpers.createNode("issueReference", token.attributes);
  },

  renderMarkdown: (node: any) => {
    const { id, label } = node.attrs || {};
    const safeLabel = (label ?? id).replace(/\[/g, "\\[").replace(/\]/g, "\\]");
    return `[${safeLabel}](mention://issue/${id})`;
  },
});

function IssueReferenceView({ node }: NodeViewProps) {
  const { id, label } = node.attrs;
  const { t } = useT("editor");
  const p = useWorkspacePaths();
  const { push, openInNewTab } = useNavigation();
  const issuePath = p.issueDetail(id);

  const handleClick = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.metaKey || e.ctrlKey || e.shiftKey) {
      if (openInNewTab) openInNewTab(issuePath, label);
      return;
    }
    push(issuePath);
  };

  return (
    <NodeViewWrapper as="span" className="inline">
      <a href={issuePath} onClick={handleClick} className="issue-mention inline-flex">
        <IssueChip
          issueId={id}
          // Identifier-only while loading; never pass title attrs here.
          fallbackLabel={typeof label === "string" && label.trim() ? label : undefined}
          unresolvedLabel={t(($) => $.issue_reference.unavailable)}
          className="cursor-pointer hover:bg-accent transition-colors"
        />
      </a>
    </NodeViewWrapper>
  );
}
