import { type RefObject, useEffect, useRef, useCallback } from "react"

/**
 * Auto-scrolls a scroll container to the bottom when its inner content grows,
 * as long as the user hasn't scrolled up to read older content.
 *
 * Returns a `lockRef` that can be set to `true` to temporarily suppress
 * auto-scroll (e.g. during history prepend operations).
 */
export function useAutoScroll(
  ref: RefObject<HTMLElement | null>,
  /** Re-bind when the scroll host mounts/unmounts (e.g. drawer open). */
  bindKey?: unknown,
) {
  const stickRef = useRef(true)
  const lockRef = useRef(false)
  // Re-running the initial scroll-to-bottom on every effect mount would
  // overwrite the scroll position any time React tears the effect down and
  // brings it back — e.g. when the host tab cycles through `<Activity
  // mode="hidden">` and back. We want the jump only on a real first mount.
  const didInitialScrollRef = useRef(false)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    // New host element (drawer reopen) should pin once to bottom again.
    didInitialScrollRef.current = false
    stickRef.current = true
    const scrollToBottom = () => {
      el.scrollTo({ top: el.scrollHeight })
    }

    const onScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = el
      stickRef.current = scrollHeight - scrollTop - clientHeight < 50
    }

    const onContentChange = () => {
      if (lockRef.current) return
      if (stickRef.current) {
        scrollToBottom()
      }
    }

    // Watch the host (tab reveal / flex height) and children (content growth).
    const ro = new ResizeObserver(onContentChange)
    ro.observe(el)
    for (const child of el.children) {
      ro.observe(child)
    }

    // Watch for added/removed child nodes (new messages rendered)
    const mo = new MutationObserver((mutations) => {
      // Also observe newly added elements
      for (const mutation of mutations) {
        for (const node of mutation.addedNodes) {
          if (node instanceof Element) {
            ro.observe(node)
          }
        }
      }
      onContentChange()
    })
    mo.observe(el, { childList: true, subtree: true })

    el.addEventListener("scroll", onScroll, { passive: true })

    if (!didInitialScrollRef.current) {
      didInitialScrollRef.current = true
      scrollToBottom()
    }

    return () => {
      el.removeEventListener("scroll", onScroll)
      ro.disconnect()
      mo.disconnect()
    }
  }, [ref, bindKey])

  /** Temporarily suppress auto-scroll during prepend operations */
  const suppressAutoScroll = useCallback(() => {
    lockRef.current = true
    return () => { lockRef.current = false }
  }, [])

  return { suppressAutoScroll }
}
