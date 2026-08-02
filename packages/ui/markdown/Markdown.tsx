import * as React from 'react'
import ReactMarkdown, { type Components, defaultUrlTransform } from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import { FileText, Download } from 'lucide-react'
import { cn } from '@multica/ui/lib/utils'
import { CODE_LIGATURE_CLASS } from '@multica/ui/lib/code-style'
import { CodeBlock, InlineCode } from './CodeBlock'
import { isAllowedFileCardHref, preprocessFileCards } from './file-cards'
import { preprocessLinks, preprocessIssueRefs } from './linkify'
import { preprocessCitationTokens, preprocessMentionShortcodes } from './mentions'
import { preprocessStickers } from './stickers'
import 'katex/dist/katex.min.css'
import './markdown.css'

type AppLinkRenderer = React.ComponentType<{ href: string; children: React.ReactNode }>

/**
 * Render modes for markdown content:
 *
 * - 'terminal': Raw output with minimal formatting, control chars visible
 *   Best for: Debug output, raw logs, when you want to see exactly what's there
 *
 * - 'minimal': Clean rendering with syntax highlighting but no extra chrome
 *   Best for: Chat messages, inline content, when you want readability without clutter
 *
 * - 'full': Rich rendering with beautiful tables, styled code blocks, proper typography
 *   Best for: Documentation, long-form content, when presentation matters
 *
 * - 'inline': ONLY inline emphasis (bold / italic / strikethrough / inline code /
 *   links) is formatted; every block-level construct (paragraphs, code fences,
 *   tables, lists, headings, quotes) is flattened to plain inline text.
 *   Best for: One-line previews of possibly-TRUNCATED text (e.g. the Activity
 *   Output summary) where a half-written block — an unclosed ``` fence, a cut
 *   table — must not render as broken block chrome and swallow the rest.
 */
export type RenderMode = 'terminal' | 'minimal' | 'full' | 'inline'

export interface MarkdownProps {
  children: string
  /**
   * Render mode controlling formatting level
   * @default 'minimal'
   */
  mode?: RenderMode
  className?: string
  /**
   * Message ID for memoization (optional)
   * When provided, memoizes parsed blocks to avoid re-parsing during streaming
   */
  id?: string
  /**
   * Callback when a URL is clicked
   */
  onUrlClick?: (url: string) => void
  /**
   * Callback when a file path is clicked
   */
  onFileClick?: (path: string) => void
  /**
   * Custom renderer for mention links (e.g. mention://issue/UUID).
   * When not provided, mentions render as a simple styled span.
   */
  renderMention?: (props: { type: string; id: string; label?: string }) => React.ReactNode
  /**
   * Custom renderer for citation tokens (e.g. cit://citation-id in report prose).
   * When provided, replaces the default link renderer for `[[cit:id]]` tokens.
   */
  renderCitation?: (props: { citationId: string; label: string }) => React.ReactNode
  /** Custom renderer for non-http app links such as Wendy's create-agent cards. */
  renderAppLink?: AppLinkRenderer
  /**
   * CDN hostname for file card detection (e.g. "multica-static.copilothub.ai").
   * When provided, enables file card preprocessing and rendering.
   */
  cdnDomain?: string
  /**
   * Optional override for the image renderer. When provided, replaces the
   * default `<img>` with constrained sizing. The views-package wrapper uses
   * this to inject the unified `<Attachment>` component so chat messages get
   * the same hover toolbar / lightbox / preview-modal treatment as comments.
   */
  renderImage?: (props: { src: string; alt: string }) => React.ReactNode
  /**
   * Optional override for the file-card renderer. When provided, replaces
   * the simplified card chrome (filename + download button) with whatever
   * the caller supplies. Used the same way as `renderImage` to bridge into
   * the views-package `<Attachment>` component.
   */
  renderFileCard?: (props: { href: string; filename: string }) => React.ReactNode
  /**
   * Workspace issue prefix (e.g. "MUL"). When set, bare issue identifiers like
   * "MUL-123" in the text are auto-linked into issue mention chips. Scoped to
   * this single prefix to avoid false positives. Omit to disable auto-linking.
   */
  issueRefPrefix?: string
  /**
   * Search phrase to highlight in rendered visible text. The match is
   * case-insensitive and does not touch code/preformatted blocks.
   */
  highlightQuery?: string
  /**
   * Convert `:sticker:<id>:` shortcodes into sticker image markdown.
   * @default true
   */
  enableStickerShortcodes?: boolean
}

// Sanitization schema — extends GitHub defaults to allow code highlighting classes
// and Multica's internal mention/slash protocols.
const sanitizeSchema = {
  ...defaultSchema,
  protocols: {
    ...defaultSchema.protocols,
    href: [...(defaultSchema.protocols?.href ?? []), 'mention', 'slash', 'multica'],
  },
  attributes: {
    ...defaultSchema.attributes,
    div: [
      ...(defaultSchema.attributes?.div ?? []),
      'dataType',
      'dataHref',
      'dataFilename',
    ],
    code: [
      ...(defaultSchema.attributes?.code ?? []),
      ['className', /^language-/],
      ['className', /^math-/],
      ['className', /^hljs/],
    ],
    img: [
      ...(defaultSchema.attributes?.img ?? []),
      'alt',
    ],
  },
}

/**
 * Custom URL transform that allows Multica internal protocols while keeping
 * the default security for all other URLs.
 */
function urlTransform(url: string): string {
  if (url.startsWith('mention://')) return url
  if (url.startsWith('slash://skill/')) return url
  if (url.startsWith('multica://')) return url
  return defaultUrlTransform(url)
}


// File path detection regex - matches paths starting with /, ~/, or ./
const FILE_PATH_REGEX =
  /^(?:\/|~\/|\.\/)[\w\-./@]+\.(?:ts|tsx|js|jsx|mjs|cjs|md|json|yaml|yml|py|go|rs|css|scss|less|html|htm|txt|log|sh|bash|zsh|swift|kt|java|c|cpp|h|hpp|rb|php|xml|toml|ini|cfg|conf|env|sql|graphql|vue|svelte|astro|prisma)$/i

const SEARCH_HIGHLIGHT_CLASS =
  'bg-primary/20 text-foreground rounded-[3px] px-0.5 box-decoration-clone'

const HIGHLIGHT_SKIP_TAGS = new Set([
  'button',
  'code',
  'img',
  'input',
  'mark',
  'pre',
  'select',
  'textarea',
])

export function normalizeHighlightQuery(query?: string): string | undefined {
  const trimmed = query?.trim()
  return trimmed ? trimmed : undefined
}

function highlightTextWithQuery(text: string, query: string): React.ReactNode {
  const lowerText = text.toLocaleLowerCase()
  const lowerQuery = query.toLocaleLowerCase()
  const nodes: React.ReactNode[] = []
  let cursor = 0
  let matchIndex = lowerText.indexOf(lowerQuery, cursor)

  while (matchIndex !== -1) {
    if (matchIndex > cursor) {
      nodes.push(text.slice(cursor, matchIndex))
    }

    const end = matchIndex + query.length
    nodes.push(
      <mark key={`hit-${matchIndex}-${nodes.length}`} className={SEARCH_HIGHLIGHT_CLASS}>
        {text.slice(matchIndex, end)}
      </mark>,
    )
    cursor = end
    matchIndex = lowerText.indexOf(lowerQuery, cursor)
  }

  if (nodes.length === 0) return text
  if (cursor < text.length) nodes.push(text.slice(cursor))
  return nodes
}

export function highlightSearchText(text: string, query?: string): React.ReactNode {
  const normalizedQuery = normalizeHighlightQuery(query)
  if (!normalizedQuery) return text
  return highlightTextWithQuery(text, normalizedQuery)
}

function shouldSkipHighlight(element: React.ReactElement): boolean {
  return typeof element.type === 'string' && HIGHLIGHT_SKIP_TAGS.has(element.type)
}

function highlightSearchChildren(children: React.ReactNode, query?: string): React.ReactNode {
  const normalizedQuery = normalizeHighlightQuery(query)
  if (!normalizedQuery) return children

  return React.Children.map(children, (child) => {
    if (typeof child === 'string') {
      return highlightTextWithQuery(child, normalizedQuery)
    }
    if (typeof child === 'number') {
      return highlightTextWithQuery(String(child), normalizedQuery)
    }
    if (!React.isValidElement<{ children?: React.ReactNode }>(child)) {
      return child
    }
    if (typeof child.type !== 'string') {
      return child
    }
    if (shouldSkipHighlight(child)) {
      return child
    }
    if (child.props.children === undefined) {
      return child
    }
    return React.cloneElement(
      child,
      undefined,
      highlightSearchChildren(child.props.children, normalizedQuery),
    )
  })
}

function extractText(children: React.ReactNode): string | undefined {
  let text = ''

  React.Children.forEach(children, (child) => {
    if (typeof child === 'string' || typeof child === 'number') {
      text += child
      return
    }
    if (!React.isValidElement<{ children?: React.ReactNode }>(child)) {
      return
    }
    const childText = extractText(child.props.children)
    if (childText) text += childText
  })

  return text || undefined
}

/**
 * Create custom components based on render mode
 */
function createComponents(
  mode: RenderMode,
  onUrlClick?: (url: string) => void,
  onFileClick?: (path: string) => void,
  renderMention?: (props: { type: string; id: string; label?: string }) => React.ReactNode,
  renderAppLink?: AppLinkRenderer,
  renderCitation?: (props: { citationId: string; label: string }) => React.ReactNode,
  renderImage?: (props: { src: string; alt: string }) => React.ReactNode,
  renderFileCard?: (props: { href: string; filename: string }) => React.ReactNode,
  highlightQuery?: string,
): Partial<Components> {
  const highlight = (children: React.ReactNode): React.ReactNode =>
    highlightSearchChildren(children, highlightQuery)

  const baseComponents: Partial<Components> = {
    // FileCard: intercept <div data-type="fileCard"> from preprocessFileCards
    div: ({ node, children, ...props }) => {
      const dataType = node?.properties?.dataType as string | undefined
      if (dataType === 'fileCard') {
        const rawHref = (node?.properties?.dataHref as string) || ''
        const href = isAllowedFileCardHref(rawHref) ? rawHref : ''
        const filename = (node?.properties?.dataFilename as string) || ''
        if (renderFileCard) {
          return <>{renderFileCard({ href, filename })}</>
        }
        return (
          <div className="my-1 flex items-center gap-2 rounded-md border border-border bg-muted/50 px-2.5 py-1 transition-colors hover:bg-muted">
            <FileText className="size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm">{filename}</p>
            </div>
            {href && (
              <button
                type="button"
                className="shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
                onClick={() => window.open(href, '_blank', 'noopener,noreferrer')}
              >
                <Download className="size-3.5" />
              </button>
            )}
          </div>
        )
      }
      return <div {...props}>{highlight(children)}</div>
    },
    // Images: render uploaded images with constrained sizing
    img: ({ src, alt }) => {
      if (renderImage) {
        return <>{renderImage({ src: typeof src === 'string' ? src : '', alt: alt ?? '' })}</>
      }
      return (
        <img
          src={src}
          alt={alt ?? ""}
          className="max-w-full h-auto rounded-md my-2"
          loading="lazy"
        />
      )
    },
    // Links: Make clickable with callbacks, or render as mention
    a: ({ href, children }) => {
      // Citation tokens: [[cit:id]] → cit://id (LRM-830)
      if (href?.startsWith('cit://')) {
        const m = href.match(/^cit:\/\/(.+)$/)
        if (m?.[1] && renderCitation) {
          return renderCitation({ citationId: m[1], label: String(children) })
        }
      }
      // Mention links: mention://member/id, mention://agent/id, mention://issue/id, mention://project/id, mention://all/all
      if (href?.startsWith('mention://')) {
        const mentionMatch = href.match(/^mention:\/\/(member|agent|issue|project|all)\/(.+)$/)
        if (mentionMatch?.[1] && mentionMatch[2]) {
          const type = mentionMatch[1]
          const id = mentionMatch[2]

          if (renderMention) {
            // Pass the link text as a label so the renderer can fall back to
            // the author's intended name when the id isn't resolvable (e.g. a
            // user who left the workspace) instead of rendering "Unknown".
            const label = extractText(children)
            // Let the custom renderer opt out for types it doesn't handle
            // by returning null/undefined — we then fall through to the
            // default styled span so nothing ever disappears silently.
            const rendered = renderMention({ type, id, label })
            if (rendered) return <>{rendered}</>
          }

          // Fallback: Slack soft-bg mention (matches views
          // mentionTokenClassName default when the host does not supply renderMention).
          return (
            <span className="mention not-prose inline rounded-sm px-0.5 font-bold box-decoration-clone bg-brand/[0.10] text-brand">
              {highlight(children)}
            </span>
          )
        }
        return (
          <span className="mention not-prose inline rounded-sm px-0.5 font-bold box-decoration-clone bg-brand/[0.10] text-brand">
            {highlight(children)}
          </span>
        )
      }

      if (href?.startsWith('slash://skill/')) {
        return (
          <span className="slash-command text-primary font-semibold mx-0.5">
            {highlight(children)}
          </span>
        )
      }

      if (href?.startsWith('multica://') && renderAppLink) {
        const AppLink = renderAppLink
        return <AppLink href={href}>{highlight(children)}</AppLink>
      }

      // #836: cancel the browser's own behaviour ONLY when a handler is
      // actually taking over. This used to call preventDefault() unconditionally
      // and then re-implement the anchor with
      // `window.open(href, '_blank', 'noopener,noreferrer')` — which is strictly
      // worse than what it replaced: it needs popup permission, it cannot detect
      // its own failure (with `noopener`, window.open returns null whether it was
      // blocked or not), and it gives no feedback either way. An attachment link
      // whose popup was blocked and one that downloaded silently are
      // indistinguishable — to the user AND to us. That is the reported bug:
      // clicking an attachment link appeared to do nothing, while the server was
      // returning a perfectly good `Content-Disposition: attachment` response.
      //
      // Verified on served (leagent.me): no view passes `onUrlClick`, so every
      // link in message content took the window.open branch.
      const handleClick = (e: React.MouseEvent): void => {
        if (!href) return
        if (FILE_PATH_REGEX.test(href) && onFileClick) {
          e.preventDefault()
          onFileClick(href)
          return
        }
        if (onUrlClick) {
          e.preventDefault()
          onUrlClick(href)
          return
        }
        // No handler: let the anchor do its own job. `target="_blank"` below is
        // a user-initiated navigation, not a popup, so nothing can silently
        // block it; a download resolves through Content-Disposition and a real
        // failure is shown by the browser instead of being swallowed.
      }

      // LRM-555/561 reading-flow: links share one brand accent (not near-black
      // primary that reads like bold prose). Soft underline keeps them distinct
      // from @mentions without competing for weight.
      return (
        <a
          href={href}
          onClick={handleClick}
          // Preserves the new-tab behaviour the old window.open call provided,
          // without needing popup permission. On desktop, Electron routes a
          // native target="_blank" through the same setWindowOpenHandler →
          // openExternalSafely path it already used for window.open, so the
          // external-link behaviour there is unchanged (main/index.ts:200).
          target="_blank"
          rel="noopener noreferrer"
          className="cursor-pointer text-brand font-medium underline decoration-brand/35 underline-offset-2 hover:decoration-brand"
        >
          {highlight(children)}
        </a>
      )
    }
  }

  // Inline mode: only inline emphasis is formatted; block nodes flatten to text.
  // The source may be a truncated fragment, so a block renderer (styled code
  // block / table) is never used — a cut ``` fence would otherwise swallow the
  // tail into broken chrome. Block nodes emit their text children inline instead.
  if (mode === 'inline') {
    // Trailing space preserves the word break at a former block boundary so
    // "a\n\nb" reads as "a b" once flattened, not "ab".
    const inlineBlock = ({ children }: { children?: React.ReactNode }) => (
      <span>{highlight(children)} </span>
    )
    return {
      ...baseComponents,
      // No images in a one-line text preview — fall back to alt text if present.
      img: ({ alt }) => (alt ? <span>{alt}</span> : null),
      // Inline code keeps its chrome; a fenced/multi-line block (incl. an unclosed
      // truncated fence) degrades to plain monospace text, never a CodeBlock.
      code: ({ className, children, ...props }) => {
        const match = /language-(\w+)/.exec(className || '')
        const isBlock =
          'node' in props && props.node?.position?.start.line !== props.node?.position?.end.line
        if (match || isBlock) {
          return (
            <span className={cn('font-mono', CODE_LIGATURE_CLASS)}>
              {String(children).replace(/\n$/, '')}
            </span>
          )
        }
        return <InlineCode>{children}</InlineCode>
      },
      pre: ({ children }) => <>{children}</>,
      p: inlineBlock,
      h1: inlineBlock,
      h2: inlineBlock,
      h3: inlineBlock,
      h4: inlineBlock,
      h5: inlineBlock,
      h6: inlineBlock,
      ul: inlineBlock,
      ol: inlineBlock,
      li: inlineBlock,
      blockquote: inlineBlock,
      table: inlineBlock,
      thead: inlineBlock,
      tbody: inlineBlock,
      tr: inlineBlock,
      th: inlineBlock,
      td: inlineBlock,
      hr: () => <span> </span>,
      strong: ({ children }) => <strong className="font-semibold">{highlight(children)}</strong>,
      em: ({ children }) => <em className="italic">{highlight(children)}</em>,
      del: ({ children }) => (
        <del className="line-through text-muted-foreground">{highlight(children)}</del>
      ),
    }
  }

  // Terminal mode: minimal formatting
  if (mode === 'terminal') {
    return {
      ...baseComponents,
      // No special code handling - just monospace
      code: ({ children }) => <code className={cn('font-mono', CODE_LIGATURE_CLASS)}>{children}</code>,
      pre: ({ children }) => (
        <pre className={cn('font-mono whitespace-pre-wrap my-2', CODE_LIGATURE_CLASS)}>
          {children}
        </pre>
      ),
      // Minimal paragraph spacing
      p: ({ children }) => <p className="my-1">{highlight(children)}</p>,
      // Simple lists
      ul: ({ children }) => <ul className="list-disc list-inside my-1">{highlight(children)}</ul>,
      ol: ({ children }) => <ol className="list-decimal list-inside my-1">{highlight(children)}</ol>,
      li: ({ children }) => <li className="my-0.5">{highlight(children)}</li>,
      // Plain tables
      table: ({ children }) => <table className="my-2 font-mono text-sm">{highlight(children)}</table>,
      th: ({ children }) => <th className="text-left pr-4">{highlight(children)}</th>,
      td: ({ children }) => <td className="pr-4">{highlight(children)}</td>
    }
  }

  // Minimal mode: clean with syntax highlighting
  if (mode === 'minimal') {
    return {
      ...baseComponents,
      // Inline code
      code: ({ className, children, ...props }) => {
        const match = /language-(\w+)/.exec(className || '')
        const isBlock =
          'node' in props && props.node?.position?.start.line !== props.node?.position?.end.line

        // Block code - use CodeBlock with full mode
        if (match || isBlock) {
          const code = String(children).replace(/\n$/, '')
          return <CodeBlock code={code} language={match?.[1]} mode="full" className="my-1" />
        }

        // Inline code
        return <InlineCode>{children}</InlineCode>
      },
      pre: ({ children }) => <>{children}</>,
      // Comfortable paragraph spacing
      p: ({ children }) => <p className="my-2 leading-relaxed">{highlight(children)}</p>,
      // Styled lists
      ul: ({ children }) => (
        <ul className="my-2 space-y-1 ps-4 pe-2 list-disc marker:text-muted-foreground">
          {highlight(children)}
        </ul>
      ),
      ol: ({ children }) => <ol className="my-2 space-y-1 pl-6 list-decimal">{highlight(children)}</ol>,
      li: ({ children }) => <li>{highlight(children)}</li>,
      // Clean tables — wrap cells (LRM-987: no ellipsis-only crop in narrow bubbles)
      table: ({ children }) => (
        <div className="my-3 max-w-full overflow-x-auto">
          <table className="w-max min-w-full text-sm">{highlight(children)}</table>
        </div>
      ),
      thead: ({ children }) => <thead className="border-b">{highlight(children)}</thead>,
      th: ({ children }) => (
        <th className="whitespace-normal break-words text-left py-2 px-3 font-semibold text-muted-foreground">
          {highlight(children)}
        </th>
      ),
      td: ({ children }) => (
        <td className="whitespace-normal break-words py-2 px-3 border-b border-border/50">
          {highlight(children)}
        </td>
      ),
      // Headings - H1/H2 same size, differentiated by weight
      h1: ({ children }) => <h1 className="font-sans text-base font-bold mt-5 mb-3">{highlight(children)}</h1>,
      h2: ({ children }) => (
        <h2 className="font-sans text-base font-semibold mt-4 mb-3">{highlight(children)}</h2>
      ),
      h3: ({ children }) => (
        <h3 className="font-sans text-sm font-semibold mt-4 mb-2">{highlight(children)}</h3>
      ),
      // Blockquotes
      blockquote: ({ children }) => (
        <blockquote className="border-l-2 border-muted-foreground/30 pl-3 my-2 text-muted-foreground italic">
          {highlight(children)}
        </blockquote>
      ),
      // Horizontal rules
      hr: () => <hr className="my-4 border-border" />,
      // Strong/emphasis
      strong: ({ children }) => <strong className="font-semibold">{highlight(children)}</strong>,
      em: ({ children }) => <em className="italic">{highlight(children)}</em>
    }
  }

  // Full mode: rich styling
  return {
    ...baseComponents,
    // Full code blocks with copy button
    code: ({ className, children, ...props }) => {
      const match = /language-(\w+)/.exec(className || '')
      const isBlock =
        'node' in props && props.node?.position?.start.line !== props.node?.position?.end.line

      if (match || isBlock) {
        const code = String(children).replace(/\n$/, '')
        return <CodeBlock code={code} language={match?.[1]} mode="full" className="my-1" />
      }

      return <InlineCode>{children}</InlineCode>
    },
    pre: ({ children }) => <>{children}</>,
    // Rich paragraph spacing
    p: ({ children }) => <p className="my-3 leading-relaxed">{highlight(children)}</p>,
    // Styled lists
    ul: ({ children }) => (
      <ul className="my-3 space-y-1.5 ps-4 pe-2 list-disc marker:text-muted-foreground">
        {highlight(children)}
      </ul>
    ),
    ol: ({ children }) => (
      <ol className="my-3 space-y-1.5 pl-6 list-decimal">{highlight(children)}</ol>
    ),
    li: ({ children }) => <li className="leading-relaxed">{highlight(children)}</li>,
    // Beautiful tables — wrap cells (LRM-987)
    table: ({ children }) => (
      <div className="my-4 max-w-full overflow-x-auto rounded-md border">
        <table className="w-max min-w-full divide-y divide-border">{highlight(children)}</table>
      </div>
    ),
    thead: ({ children }) => <thead className="bg-muted/50">{highlight(children)}</thead>,
    tbody: ({ children }) => <tbody className="divide-y divide-border">{highlight(children)}</tbody>,
    th: ({ children }) => (
      <th className="whitespace-normal break-words text-left py-3 px-4 font-semibold text-sm">
        {highlight(children)}
      </th>
    ),
    td: ({ children }) => (
      <td className="whitespace-normal break-words py-3 px-4 text-sm">{highlight(children)}</td>
    ),
    tr: ({ children }) => (
      <tr className="hover:bg-muted/30 transition-colors">{highlight(children)}</tr>
    ),
    // Rich headings
    h1: ({ children }) => (
      <h1 className="font-sans text-base font-bold mt-7 mb-4">{highlight(children)}</h1>
    ),
    h2: ({ children }) => (
      <h2 className="font-sans text-base font-semibold mt-6 mb-3">{highlight(children)}</h2>
    ),
    h3: ({ children }) => (
      <h3 className="font-sans text-sm font-semibold mt-5 mb-3">{highlight(children)}</h3>
    ),
    h4: ({ children }) => (
      <h4 className="text-sm font-semibold mt-3 mb-1">{highlight(children)}</h4>
    ),
    // Styled blockquotes
    blockquote: ({ children }) => (
      <blockquote className="border-l-4 border-foreground/30 bg-muted/30 pl-4 pr-3 py-2 my-3 rounded-r-md">
        {highlight(children)}
      </blockquote>
    ),
    // Task lists (GFM)
    input: ({ type, checked }) => {
      if (type === 'checkbox') {
        return (
          <input
            type="checkbox"
            checked={checked}
            readOnly
            className="mr-2 rounded border-muted-foreground"
          />
        )
      }
      return <input type={type} />
    },
    // Horizontal rules
    hr: () => <hr className="my-6 border-border" />,
    // Strong/emphasis
    strong: ({ children }) => <strong className="font-semibold">{highlight(children)}</strong>,
    em: ({ children }) => <em className="italic">{highlight(children)}</em>,
    del: ({ children }) => (
      <del className="line-through text-muted-foreground">{highlight(children)}</del>
    )
  }
}

/**
 * Markdown - Customizable markdown renderer with multiple render modes
 *
 * Features:
 * - Three render modes: terminal, minimal, full
 * - Syntax highlighting via Shiki
 * - GFM support (tables, task lists, strikethrough)
 * - Clickable links and file paths
 * - Memoization for streaming performance
 * - Pluggable mention rendering via renderMention prop
 */
export function Markdown({
  children,
  mode = 'minimal',
  className,
  onUrlClick,
  onFileClick,
  renderMention,
  renderAppLink,
  renderCitation,
  renderImage,
  renderFileCard,
  cdnDomain,
  issueRefPrefix,
  highlightQuery,
  enableStickerShortcodes = true
}: MarkdownProps): React.JSX.Element {
  const normalizedHighlightQuery = normalizeHighlightQuery(highlightQuery)
  const components = React.useMemo(
    () =>
      createComponents(
        mode,
        onUrlClick,
        onFileClick,
        renderMention,
        renderAppLink,
        renderCitation,
        renderImage,
        renderFileCard,
        normalizedHighlightQuery,
      ),
    [mode, onUrlClick, onFileClick, renderMention, renderAppLink, renderCitation, renderImage, renderFileCard, normalizedHighlightQuery]
  )

  // Preprocess: convert mention shortcodes, citation tokens, bare issue
  // identifiers, raw URLs, and file cards to renderable content
  const processedContent = React.useMemo(
    () => {
      let result = preprocessMentionShortcodes(children)
      result = preprocessCitationTokens(result)
      if (enableStickerShortcodes) result = preprocessStickers(result)
      if (issueRefPrefix) result = preprocessIssueRefs(result, issueRefPrefix)
      result = preprocessLinks(result)
      result = preprocessFileCards(result, cdnDomain ?? '')
      return result
    },
    [children, cdnDomain, enableStickerShortcodes, issueRefPrefix]
  )

  const tree = (
    <ReactMarkdown
      remarkPlugins={[remarkMath, remarkBreaks, [remarkGfm, { singleTilde: false }]]}
      rehypePlugins={[rehypeRaw, [rehypeSanitize, sanitizeSchema], rehypeKatex]}
      urlTransform={urlTransform}
      components={components}
    >
      {processedContent}
    </ReactMarkdown>
  )

  // Inline mode renders into a <span> (not a block <div>) so its only consumer —
  // the inline-reference projector, which renders one Markdown per text run
  // between reference tokens — flows every run + token on the SAME line instead
  // of forcing each run onto its own row (the #601 block-break regression).
  if (mode === 'inline') {
    return (
      <span className={cn('markdown-content markdown-content-inline break-words', className)}>
        {tree}
      </span>
    )
  }

  return <div className={cn('markdown-content break-words', className)}>{tree}</div>
}

/**
 * MemoizedMarkdown - Optimized for streaming scenarios
 *
 * Splits content into blocks and memoizes each block separately,
 * so only new/changed blocks re-render during streaming.
 */
export const MemoizedMarkdown = React.memo(Markdown, (prevProps, nextProps) => {
  // If id is provided, use it for memoization
  if (prevProps.id && nextProps.id) {
    return (
      prevProps.id === nextProps.id &&
      prevProps.children === nextProps.children &&
      prevProps.mode === nextProps.mode &&
      prevProps.highlightQuery === nextProps.highlightQuery
    )
  }
  // Otherwise compare content and mode
  return (
    prevProps.children === nextProps.children &&
    prevProps.mode === nextProps.mode &&
    prevProps.highlightQuery === nextProps.highlightQuery
  )
})
MemoizedMarkdown.displayName = 'MemoizedMarkdown'
