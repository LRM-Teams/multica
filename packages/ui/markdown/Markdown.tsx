import * as React from 'react'
import { lazy, Suspense } from 'react'
import { cn } from '@multica/ui/lib/utils'
import type { MarkdownProps as RichMarkdownProps } from './markdown-rich'

export type { RenderMode } from './markdown-rich'
export type MarkdownProps = RichMarkdownProps

/**
 * LRM-1264 R4 — keep react-markdown / remark / rehype / CodeBlock off the
 * resting chat graph. Plain prose renders here; anything structured lazy-loads
 * the rich pipeline (same visual result once the chunk resolves).
 */
function isPlainChatProse(text: string, issueRefPrefix?: string): boolean {
  if (!text) return true
  if (/[`*_~[\]#>|\\]/.test(text)) return false
  if (/https?:\/\//i.test(text) || /mention:\/\//i.test(text) || /cit:\/\//i.test(text)) {
    return false
  }
  if (/@[\w.-]/.test(text)) return false
  if (/:\w+:/.test(text)) return false
  if (/\$\$|\\\(|\\\[/.test(text)) return false
  if (issueRefPrefix) {
    const escaped = issueRefPrefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    if (new RegExp(`\\b${escaped}-\\d+\\b`, 'i').test(text)) return false
  }
  return true
}

const SEARCH_HIGHLIGHT_CLASS =
  'bg-primary/20 text-foreground rounded-[3px] px-0.5 box-decoration-clone'

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

/** Shared so tests can await chunk resolve before asserting rich output. */
export const markdownRichReady = import('./markdown-rich')

const MarkdownRich = lazy(() =>
  markdownRichReady.then((m) => ({ default: m.MarkdownRich })),
)

function PlainMarkdown({
  children,
  mode = 'minimal',
  className,
  highlightQuery,
}: Pick<MarkdownProps, 'children' | 'mode' | 'className' | 'highlightQuery'>): React.JSX.Element {
  const body = highlightSearchText(children, normalizeHighlightQuery(highlightQuery))
  if (mode === 'inline') {
    return (
      <span
        className={cn(
          'markdown-content markdown-content-inline break-words whitespace-pre-wrap',
          className,
        )}
      >
        {body}
      </span>
    )
  }
  return (
    <div className={cn('markdown-content break-words whitespace-pre-wrap', className)}>
      {body}
    </div>
  )
}

export function Markdown(props: MarkdownProps): React.JSX.Element {
  const {
    children,
    mode = 'minimal',
    className,
    cdnDomain,
    issueRefPrefix,
    highlightQuery,
  } = props

  const usePlainProse =
    (mode === 'minimal' || mode === 'full' || mode === 'inline') &&
    !cdnDomain &&
    isPlainChatProse(children, issueRefPrefix)

  if (usePlainProse) {
    return (
      <PlainMarkdown mode={mode} className={className} highlightQuery={highlightQuery}>
        {children}
      </PlainMarkdown>
    )
  }

  // While the rich chunk loads, keep a reserved markdown shell (not the plain
  // gate) so structured tokens are not falsely painted as escaped prose.
  return (
    <Suspense
      fallback={
        <div
          className={cn(
            mode === 'inline'
              ? 'markdown-content markdown-content-inline break-words'
              : 'markdown-content break-words',
            className,
          )}
          aria-busy="true"
        />
      }
    >
      <MarkdownRich {...props} />
    </Suspense>
  )
}

/**
 * MemoizedMarkdown - Optimized for streaming scenarios
 */
export const MemoizedMarkdown = React.memo(Markdown, (prevProps, nextProps) => {
  if (prevProps.id && nextProps.id) {
    return (
      prevProps.id === nextProps.id &&
      prevProps.children === nextProps.children &&
      prevProps.mode === nextProps.mode &&
      prevProps.highlightQuery === nextProps.highlightQuery
    )
  }
  return (
    prevProps.children === nextProps.children &&
    prevProps.mode === nextProps.mode &&
    prevProps.highlightQuery === nextProps.highlightQuery
  )
})
MemoizedMarkdown.displayName = 'MemoizedMarkdown'
