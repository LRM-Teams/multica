import { Node, mergeAttributes } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import { Suggestion, type SuggestionOptions } from "@tiptap/suggestion";
import { PluginKey } from "@tiptap/pm/state";
import type { MentionItem } from "./mention-suggestion";
import { ChannelReferenceView } from "./channel-reference-view";

// Mirrors IssueReferenceExtension's shape (issue-reference.tsx) — same
// atom-node + markdown round-trip + click-to-navigate pattern, swapped to
// channels. Kept as a separate node (not folded into IssueReferenceExtension)
// so the two can round-trip independently through existing message content:
// an issueReference node in an old message must keep parsing as an issue
// even after this node type is introduced.
export const ChannelReferenceExtension = Node.create({
  name: "channelReference",
  group: "inline",
  inline: true,
  atom: true,
  selectable: false,

  addOptions() {
    return {
      // Explicit pluginKey — see issueReference's addOptions() for why: two
      // Suggestion-backed nodes both left at their bare default otherwise
      // collide on @tiptap/suggestion's shared PluginKey("suggestion").
      suggestion: {
        char: "#",
        allow: () => false,
        pluginKey: new PluginKey("channelReferenceSuggestionDisabled"),
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
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[data-channel-reference-id]",
        getAttrs: (el) => {
          if (!(el instanceof HTMLElement)) return false;
          return {
            id: el.getAttribute("data-channel-reference-id"),
            label: el.getAttribute("data-channel-reference-label"),
          };
        },
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        "data-channel-reference-id": node.attrs.id,
        "data-channel-reference-label": node.attrs.label,
      }),
      node.attrs.label ?? node.attrs.id,
    ];
  },

  addNodeView() {
    return ReactNodeViewRenderer(ChannelReferenceView);
  },

  markdownTokenizer: {
    name: "channelReference",
    level: "inline" as const,
    start(src: string) {
      return src.search(/\[(?:\\.|[^\]])+\]\(mention:\/\/channel\//);
    },
    tokenize(src: string) {
      const match = src.match(/^\[((?:\\.|[^\]])+)\]\(mention:\/\/channel\/([^)]+)\)/);
      if (!match) return undefined;
      const rawLabel = match[1]?.replace(/\\\[/g, "[").replace(/\\\]/g, "]");
      return {
        type: "channelReference",
        raw: match[0],
        attributes: { label: rawLabel, id: match[2] },
      };
    },
  },

  parseMarkdown: (token: any, helpers: any) => {
    return helpers.createNode("channelReference", token.attributes);
  },

  renderMarkdown: (node: any) => {
    const { id, label } = node.attrs || {};
    const safeLabel = (label ?? id).replace(/\[/g, "\\[").replace(/\]/g, "\\]");
    return `[${safeLabel}](mention://channel/${id})`;
  },
});
