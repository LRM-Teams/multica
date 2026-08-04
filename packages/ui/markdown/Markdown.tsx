import * as React from "react"
import { lazy, Suspense } from "react"
import { cn } from "@multica/ui/lib/utils"
import type { MarkdownProps, RenderMode } from "./markdown-rich"
export type { MarkdownProps, RenderMode }
import {
  highlightSearchText,
  isPlainChatProse,
  normalizeHighlightQuery,
} from "./markdown-text"
import { markdownRichReady } from "./markdown-rich-ready"

/**
 * LRM-1264 R4 — keep react-markdown / remark / rehype / CodeBlock off the
 * resting chat graph. Plain prose renders here; anything structured lazy-loads
 * the rich pipeline (same visual result once the chunk resolves).
 */
const MarkdownRich = lazy(() =>
  markdownRichReady.then((m) => ({ default: m.MarkdownRich })),
)

function PlainMarkdown({
  children,
  mode = "minimal",
  className,
  highlightQuery,
}: Pick<MarkdownProps, "children" | "mode" | "className" | "highlightQuery">): React.JSX.Element {
  const body = highlightSearchText(children, normalizeHighlightQuery(highlightQuery))
  if (mode === "inline") {
    return (
      <span
        className={cn(
          "markdown-content markdown-content-inline break-words whitespace-pre-wrap",
          className,
        )}
      >
        {body}
      </span>
    )
  }
  return (
    <div className={cn("markdown-content break-words whitespace-pre-wrap", className)}>
      {body}
    </div>
  )
}

export function Markdown(props: MarkdownProps): React.JSX.Element {
  const {
    children,
    mode = "minimal",
    className,
    cdnDomain,
    issueRefPrefix,
    highlightQuery,
  } = props

  const usePlainProse =
    (mode === "minimal" || mode === "full" || mode === "inline") &&
    !cdnDomain &&
    isPlainChatProse(children, issueRefPrefix)

  if (usePlainProse) {
    return (
      <PlainMarkdown mode={mode} className={className} highlightQuery={highlightQuery}>
        {children}
      </PlainMarkdown>
    )
  }

  return (
    <Suspense
      fallback={
        <div
          className={cn(
            mode === "inline"
              ? "markdown-content markdown-content-inline break-words"
              : "markdown-content break-words",
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
MemoizedMarkdown.displayName = "MemoizedMarkdown"
