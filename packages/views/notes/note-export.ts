import katex from "katex";
import { noteFormatExportCss, sanitizeTextStyle, type NoteFormatDefaults } from "@multica/core/notes/format";
import type { NotePage } from "@multica/core/types";

const KATEX_CSS_HREF = "https://cdn.jsdelivr.net/npm/katex@0.16.45/dist/katex.min.css";
const INLINE_MATH_RE = /(?<!\$)\$(?!\$)((?:\\.|[^$\\\n])+)\$(?!\$)/g;
const BLOCK_MATH_RE = /\$\$([\s\S]+?)\$\$/g;

export function escapeHtml(value: string) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

export function safeExportFilename(title: string, extension: string) {
  const basename = (title || "Untitled")
    .trim()
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/\s+/g, " ")
    .slice(0, 80) || "Untitled";
  return `${basename}.${extension}`;
}

function renderMath(expression: string, displayMode: boolean) {
  return katex.renderToString(expression, {
    displayMode,
    output: "htmlAndMathml",
    strict: "warn",
    throwOnError: false,
  });
}

function renderStyledInner(value: string) {
  let text = escapeHtml(value);
  text = text.replace(/`([^`]+)`/g, "<code>$1</code>");
  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  return text;
}

export function renderInlineMarkdown(value: string) {
  const styleTokens: { style: string; inner: string }[] = [];
  const prepared = value.replace(/<span style="([^"]*)">([\s\S]*?)<\/span>/g, (_match, style: string, inner: string) => {
    const attrs = sanitizeTextStyle(style);
    const parts: string[] = [];
    if (attrs.color) parts.push(`color: ${attrs.color}`);
    if (attrs.fontSize) parts.push(`font-size: ${attrs.fontSize}`);
    const token = `@@NOTE_STYLE_${styleTokens.length}@@`;
    styleTokens.push({ style: parts.join("; "), inner });
    return token;
  });

  const protectedTokens: string[] = [];
  let working = prepared.replace(/`([^`]+)`/g, (_match, code: string) => {
    const token = `@@NOTE_CODE_${protectedTokens.length}@@`;
    protectedTokens.push(`<code>${escapeHtml(code)}</code>`);
    return token;
  });
  working = working.replace(INLINE_MATH_RE, (_match, expression: string) => {
    const token = `@@NOTE_MATH_${protectedTokens.length}@@`;
    protectedTokens.push(renderMath(expression, false));
    return token;
  });

  const tokens: string[] = [];
  let text = escapeHtml(working);
  text = text.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_match, alt: string, src: string) => {
    const token = `@@NOTE_IMAGE_${tokens.length}@@`;
    tokens.push(`<img src="${escapeHtml(src)}" alt="${escapeHtml(alt)}" />`);
    return token;
  });
  text = text.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_match, label: string, href: string) => {
    const token = `@@NOTE_LINK_${tokens.length}@@`;
    tokens.push(`<a href="${escapeHtml(href)}">${label}</a>`);
    return token;
  });
  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  tokens.forEach((tokenHtml, index) => {
    text = text.replace(`@@NOTE_IMAGE_${index}@@`, tokenHtml).replace(`@@NOTE_LINK_${index}@@`, tokenHtml);
  });
  styleTokens.forEach((token, index) => {
    const inner = renderStyledInner(token.inner);
    const html = token.style ? `<span style="${token.style}">${inner}</span>` : inner;
    text = text.replace(`@@NOTE_STYLE_${index}@@`, html);
  });
  protectedTokens.forEach((tokenHtml, index) => {
    text = text.replace(`@@NOTE_CODE_${index}@@`, tokenHtml).replace(`@@NOTE_MATH_${index}@@`, tokenHtml);
  });
  return text;
}

function renderNoteBlocks(content: string) {
  const lines = content.split(/\r?\n/);
  const html: string[] = [];
  let paragraph: string[] = [];
  let list: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    html.push(`<p>${renderInlineMarkdown(paragraph.join(" "))}</p>`);
    paragraph = [];
  };
  const flushList = () => {
    if (list.length === 0) return;
    html.push(`<ul>${list.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("")}</ul>`);
    list = [];
  };

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = /^(#{1,3})\s+(.+)$/.exec(trimmed);
    if (heading) {
      flushParagraph();
      flushList();
      const level = heading[1]?.length ?? 1;
      html.push(`<h${level}>${renderInlineMarkdown(heading[2] ?? "")}</h${level}>`);
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      list.push(bullet[1] ?? "");
      continue;
    }
    flushList();
    paragraph.push(trimmed);
  }
  flushParagraph();
  flushList();
  return html.join("\n");
}

export function renderNoteMarkdown(content: string) {
  const parts: string[] = [];
  let lastIndex = 0;
  for (const match of content.matchAll(BLOCK_MATH_RE)) {
    const start = match.index ?? 0;
    parts.push(renderNoteBlocks(content.slice(lastIndex, start)));
    parts.push(`<div class="math-block">${renderMath(match[1]?.trim() ?? "", true)}</div>`);
    lastIndex = start + match[0].length;
  }
  parts.push(renderNoteBlocks(content.slice(lastIndex)));
  return parts.filter(Boolean).join("\n");
}

type CssRuleLike = { cssText: string };
type StyleSheetLike = { cssRules?: ArrayLike<CssRuleLike> | null };

export type NoteExportHtmlOptions = {
  extraHead?: string;
  katexCss?: string;
};

function isKatexCss(text: string) {
  return text.includes("katex") || text.includes("KaTeX");
}

export function collectKatexExportCss(styleSheets: ArrayLike<StyleSheetLike>): string {
  const chunks: string[] = [];
  for (let i = 0; i < styleSheets.length; i += 1) {
    const sheet = styleSheets[i];
    if (!sheet) continue;
    let rules: ArrayLike<CssRuleLike> | null | undefined;
    try {
      rules = sheet.cssRules;
    } catch {
      continue;
    }
    if (!rules) continue;
    for (let j = 0; j < rules.length; j += 1) {
      const text = rules[j]?.cssText ?? "";
      if (isKatexCss(text)) chunks.push(text);
    }
  }
  return chunks.join("\n");
}

export function collectKatexExportCssFromDocument(doc: Document): string {
  const fromSheets = collectKatexExportCss(doc.styleSheets);
  if (fromSheets) return fromSheets;
  return [...doc.querySelectorAll("style")]
    .map((node) => node.textContent ?? "")
    .filter(isKatexCss)
    .join("\n");
}

export function noteExportBaseHref(origin: string) {
  const base = origin.endsWith("/") ? origin : `${origin}/`;
  return `<base href="${escapeHtml(base)}" />`;
}

export function waitForNoteExportAssets(doc: Document, timeoutMs = 4000): Promise<void> {
  const links = [...doc.querySelectorAll('link[rel="stylesheet"]')] as HTMLLinkElement[];
  const stylesheets = Promise.all(
    links.map(
      (link) =>
        new Promise<void>((resolve) => {
          if (link.sheet) {
            resolve();
            return;
          }
          const finish = () => resolve();
          link.addEventListener("load", finish, { once: true });
          link.addEventListener("error", finish, { once: true });
        }),
    ),
  );
  const fonts = doc.fonts?.ready ?? Promise.resolve();
  return Promise.race([
    Promise.all([stylesheets, fonts]).then(() => undefined),
    new Promise<void>((resolve) => {
      setTimeout(resolve, timeoutMs);
    }),
  ]);
}

export function buildNoteExportHtml(
  page: NotePage,
  format: NoteFormatDefaults,
  options?: NoteExportHtmlOptions,
) {
  const title = escapeHtml(page.title || "Untitled");
  const katexHead = options?.katexCss
    ? `<style>${options.katexCss}</style>`
    : `<link rel="stylesheet" href="${KATEX_CSS_HREF}" crossorigin="anonymous" />`;
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<title>${title}</title>
${options?.extraHead ?? ""}
${katexHead}
<style>
  ${noteFormatExportCss(format)}
  h1 { font-size: 40px; line-height: 1.15; margin: 0 0 28px; }
  h2, h3 { margin-top: 28px; }
  p { margin: 14px 0; }
  img { border-radius: 8px; display: block; height: auto; margin: 18px 0; max-width: 100%; }
  code { background: #f3f4f6; border-radius: 4px; padding: 2px 5px; }
  a { color: #2563eb; }
  .math-block { margin: 18px 0; overflow: visible; }
  .katex-display { margin: 0; }
  @media print { body { margin: 0 auto; } }
</style>
</head>
<body>
<h1>${title}</h1>
${renderNoteMarkdown(page.content)}
</body>
</html>`;
}
