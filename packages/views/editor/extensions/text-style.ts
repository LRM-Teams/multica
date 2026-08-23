import { Mark, mergeAttributes } from "@tiptap/core";
import { sanitizeTextStyle } from "@multica/core/notes/format";

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    noteTextStyle: {
      setTextColor: (color: string) => ReturnType;
      unsetTextColor: () => ReturnType;
      setFontSize: (fontSize: string) => ReturnType;
      unsetFontSize: () => ReturnType;
    };
  }
}

function styleAttributeString(attrs: { color?: string | null; fontSize?: string | null }): string {
  const parts: string[] = [];
  if (attrs.color) parts.push(`color: ${attrs.color}`);
  if (attrs.fontSize) parts.push(`font-size: ${attrs.fontSize}`);
  return parts.join("; ");
}

/**
 * Notes-only inline color / font-size mark.
 *
 * Persisted as a sanitized `<span style="color: …; font-size: …">` so the
 * markdown file stays readable and the readonly / export paths can keep the
 * same styles. Unknown CSS (urls, expressions, arbitrary sizes) is dropped.
 */
export const TextStyleExtension = Mark.create({
  name: "textStyle",

  addAttributes() {
    return {
      color: {
        default: null,
        parseHTML: (element: HTMLElement) => sanitizeTextStyle(element.getAttribute("style") ?? "").color ?? null,
        rendered: false,
      },
      fontSize: {
        default: null,
        parseHTML: (element: HTMLElement) => sanitizeTextStyle(element.getAttribute("style") ?? "").fontSize ?? null,
        rendered: false,
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span",
        getAttrs: (node) => {
          if (!(node instanceof HTMLElement)) return false;
          const attrs = sanitizeTextStyle(node.getAttribute("style") ?? "");
          if (!attrs.color && !attrs.fontSize) return false;
          return attrs;
        },
      },
    ];
  },

  renderHTML({ mark }) {
    const style = styleAttributeString(mark.attrs);
    return ["span", mergeAttributes(style ? { style } : {}), 0];
  },

  markdownTokenizer: {
    name: "textStyle",
    level: "inline" as const,
    start(src: string) {
      return src.indexOf("<span style=\"");
    },
    tokenize(src: string, _tokens: unknown, helpers: any) {
      const open = src.match(/^<span style="([^"]*)">/);
      if (!open) return undefined;
      const attrs = sanitizeTextStyle(open[1] ?? "");
      if (!attrs.color && !attrs.fontSize) return undefined;
      const close = "</span>";
      const closeIdx = src.indexOf(close, open[0].length);
      if (closeIdx === -1) return undefined;
      const inner = src.slice(open[0].length, closeIdx);
      return {
        type: "textStyle",
        raw: src.slice(0, closeIdx + close.length),
        color: attrs.color ?? null,
        fontSize: attrs.fontSize ?? null,
        tokens: helpers.inlineTokens(inner),
      };
    },
  },

  parseMarkdown: (token: any, helpers: any) =>
    helpers.applyMark(
      "textStyle",
      helpers.parseInline(token.tokens),
      { color: token.color ?? null, fontSize: token.fontSize ?? null },
    ),

  renderMarkdown: (node: any, helpers: any) => {
    const style = styleAttributeString(node.attrs ?? {});
    if (!style) return helpers.renderChildren();
    return `<span style="${style}">${helpers.renderChildren()}</span>`;
  },

  addCommands() {
    return {
      setTextColor:
        (color: string) =>
        ({ chain }) => {
          const sanitized = sanitizeTextStyle(`color: ${color}`).color;
          if (!sanitized) return false;
          return chain().setMark(this.name, { color: sanitized }).run();
        },
      unsetTextColor:
        () =>
        ({ chain, editor }) => {
          const fontSize = editor.getAttributes(this.name).fontSize as string | null;
          if (fontSize) return chain().setMark(this.name, { color: null }).run();
          return chain().unsetMark(this.name).run();
        },
      setFontSize:
        (fontSize: string) =>
        ({ chain }) => {
          const sanitized = sanitizeTextStyle(`font-size: ${fontSize}`).fontSize;
          if (!sanitized) return false;
          return chain().setMark(this.name, { fontSize: sanitized }).run();
        },
      unsetFontSize:
        () =>
        ({ chain, editor }) => {
          const color = editor.getAttributes(this.name).color as string | null;
          if (color) return chain().setMark(this.name, { fontSize: null }).run();
          return chain().unsetMark(this.name).run();
        },
    };
  },
});
