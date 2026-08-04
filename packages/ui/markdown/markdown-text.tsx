import * as React from "react"

const SEARCH_HIGHLIGHT_CLASS =
  "bg-primary/20 text-foreground rounded-[3px] px-0.5 box-decoration-clone"

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

/** LRM-1264 R4 — skip react-markdown for ordinary chat rows. */
export function isPlainChatProse(text: string, issueRefPrefix?: string): boolean {
  if (!text) return true
  if (/[`*_~[\]#>|\\]/.test(text)) return false
  if (/https?:\/\//i.test(text) || /mention:\/\//i.test(text) || /cit:\/\//i.test(text)) {
    return false
  }
  if (/@[\w.-]/.test(text)) return false
  if (/:\w+:/.test(text)) return false
  if (/\$\$|\\\(|\\\[/.test(text)) return false
  if (issueRefPrefix) {
    const escaped = issueRefPrefix.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
    if (new RegExp(`\\b${escaped}-\\d+\\b`, "i").test(text)) return false
  }
  return true
}
