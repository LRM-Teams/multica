import Mention from "@tiptap/extension-mention";
import { mergeAttributes } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import * as React from "react";
import type { MentionTokenVariant } from "../../common/mention-token";
import type { MentionOptions, MentionNodeAttrs } from "@tiptap/extension-mention";
import { MentionView } from "./mention-view";

type BaseMentionOptions = MentionOptions<any, MentionNodeAttrs> & {
  /** LRM-1386 — `plain` renders non-pill inline @mentions (chat composer). */
  mentionVariant: MentionTokenVariant;
};

export const BaseMentionExtension = Mention.extend<BaseMentionOptions>({
  addOptions() {
    return {
      ...this.parent?.(),
      // LRM-1386 — chat composer passes `plain` (non-pill inline @mention);
      // issue/comment editors keep the default `soft-bg` pill.
      mentionVariant: "soft-bg" as MentionTokenVariant,
      // tiptap's MentionOptions types HTMLAttributes as required, but the
      // parent spread yields it optional — force the asserted full option set.
    } as BaseMentionOptions;
  },
  addNodeView() {
    const variant: MentionTokenVariant = this.options.mentionVariant;
    return ReactNodeViewRenderer((props) =>
      React.createElement(MentionView, { ...props, mentionVariant: variant }),
    );
  },
  renderHTML({ node, HTMLAttributes }) {
    const type = node.attrs.type ?? "member";
    const prefix = type === "project" ? "" : "@";
    return [
      "span",
      mergeAttributes(
        { "data-type": "mention" },
        this.options.HTMLAttributes,
        HTMLAttributes,
        {
          "data-mention-type": node.attrs.type ?? "member",
          "data-mention-id": node.attrs.id,
        },
      ),
      `${prefix}${node.attrs.label ?? node.attrs.id}`,
    ];
  },
  addAttributes() {
    return {
      ...this.parent?.(),
      type: {
        default: "member",
        parseHTML: (el: HTMLElement) =>
          el.getAttribute("data-mention-type") ?? "member",
        renderHTML: () => ({}),
      },
      // The actor's unique routing handle (#42, = actor `name`). The picker
      // sets it on member/agent items; it is what we serialize as bare `@handle`
      // on send (#600 rejects the legacy `mention://` syntax, so the wire form
      // is plain `@handle` that the server parses into a structured ref + span).
      // Declared here so ProseMirror keeps it on the node — otherwise the
      // default Mention command drops it as an undeclared attribute.
      handle: {
        default: null,
        parseHTML: (el: HTMLElement) =>
          el.getAttribute("data-mention-handle") ?? null,
        renderHTML: (attrs: { handle?: string | null }) =>
          attrs.handle ? { "data-mention-handle": attrs.handle } : {},
      },
    };
  },
  markdownTokenizer: {
    name: "mention",
    level: "inline" as const,
    start(src: string) {
      // Accept escaped brackets (\\[ \\]) and non-] chars in the label.
      // This prevents matching ordinary Markdown links like [docs](url)
      // that appear before a mention on the same line.
      return src.search(/\[@?(?:\\.|[^\]])+\]\(mention:\/\/(member|agent|squad|project|all)\//);
    },
    tokenize(src: string) {
      // Label accepts escaped chars (\\[ \\]) or any non-] character.
      // This prevents the label from crossing a ]( Markdown link boundary
      // while still supporting bracket-containing names like "David\[TF\]".
      const match = src.match(
        /^\[@?((?:\\.|[^\]])+)\]\(mention:\/\/(member|agent|squad|project|all)\/([^)]+)\)/,
      );
      if (!match) return undefined;
      // Unescape backslash-escaped brackets that renderMarkdown may produce.
      const rawLabel = match[1]?.replace(/\\\[/g, "[").replace(/\\\]/g, "]");
      return {
        type: "mention",
        raw: match[0],
        attributes: { label: rawLabel, type: match[2] ?? "member", id: match[3] },
      };
    },
  },
  parseMarkdown: (token: any, helpers: any) => {
    return helpers.createNode("mention", token.attributes);
  },
  renderMarkdown: (node: any) => {
    const { id, label, handle, type = "member" } = node.attrs || {};
    if (type === "project" || type === "issue") {
      // Non-actor references are out of the #600 channel-actor cutover (Barry:
      // the issue-comment surface has no MessagePart contract yet), so they keep
      // the legacy link form. In practice the `@` picker no longer produces
      // these (task #57 routes issues/projects through the `#` picker).
      const prefix = type === "project" ? "" : "@";
      const safeLabel = (label ?? id).replace(/\[/g, "\\[").replace(/\]/g, "\\]");
      return `[${prefix}${safeLabel}](mention://${type}/${id})`;
    }
    // Actor mentions (member/agent/squad/all) serialize as bare `@handle` plain
    // text — #600 hard-rejects the legacy `mention://` actor syntax, and the
    // server (#446/#463) parses the bare `@<name>` into a structured reference
    // anchored to a UTF-16 span. `handle` is the actor's unique routing name
    // (#42); the label fallback only covers legacy nodes (e.g. parsed from an
    // old `mention://` draft) that predate the handle attribute. `@all` no
    // longer broadcasts (server drops it, picker no longer offers it) so a
    // legacy @all node degrades to the literal word; squad isn't parsed
    // server-side yet either — both emit as plain text, never `mention://`.
    return `@${handle ?? label ?? id}`;
  },
});
