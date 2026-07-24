import * as React from "react"

const MOBILE_BREAKPOINT = 768
const MOBILE_QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`

function subscribeMobile(onChange: () => void) {
  const mql = window.matchMedia(MOBILE_QUERY)
  mql.addEventListener("change", onChange)
  return () => mql.removeEventListener("change", onChange)
}

function getMobileSnapshot() {
  return window.innerWidth < MOBILE_BREAKPOINT
}

function getServerMobileSnapshot() {
  return false
}

/**
 * Viewport mobile breakpoint (`width < 768`).
 *
 * Uses `useSyncExternalStore` so the first client render already sees the
 * real viewport width — the previous `useState(undefined)` + `useEffect`
 * path returned `false` for one frame (`!!undefined`), which made DM chat
 * bubbles open as the desktop floating window (min 560×480) on phones.
 */
export function useIsMobile() {
  return React.useSyncExternalStore(
    subscribeMobile,
    getMobileSnapshot,
    getServerMobileSnapshot,
  )
}

/**
 * Container-width sibling of `useIsMobile` — same boolean contract (`width <
 * breakpoint`), but the width comes from a specific element's own rendered
 * box (via `ResizeObserver`), not `window.innerWidth`.
 *
 * #568: a global viewport breakpoint is the wrong signal for layouts where
 * the observed element's width can diverge from the viewport in either
 * direction — e.g. a resizable multi-pane layout where a divider is
 * user-draggable, or a docked side panel can open and squeeze a sibling
 * pane. Measuring the element that actually renders the content in question
 * (or an ancestor whose box tracks it) reacts correctly to both a viewport
 * resize AND a sibling panel opening/closing, since both ultimately show up
 * as a change to the observed element's own rendered box.
 *
 * The `breakpoint` argument must be a fixed, independently-derived width
 * requirement (e.g. the measured natural/minimum width the caller's content
 * needs without collapsing) — never something computed from the observed
 * element's OWN current post-decision size (that would be self-referential:
 * once content collapses to a narrower "compact" form, its own box shrinks,
 * which could make the very next observer tick decide there's "room again"
 * and flip back, thrashing at the boundary).
 *
 * Returns a callback ref (not a `RefObject`) so it works even when the
 * measured element mounts after this hook's first render — a `useEffect`
 * keyed on a stable `RefObject` would only ever see the pre-mount `null`.
 * A callback ref fires exactly when the DOM node attaches (or is replaced/
 * unmounted), so the internal effect always observes the real node.
 *
 * Measures in `useLayoutEffect`, not `useEffect`: a plain effect runs AFTER
 * the browser paints, so a narrow-container mount would flash the "plenty
 * of room" (direct) branch for one visible frame before flipping to
 * compact. `useLayoutEffect` fires synchronously after DOM mutations but
 * before the browser paints, so the corrected value lands in the same
 * commit the user actually sees — no flash.
 */
export function useContainerNarrowerThan(breakpoint: number) {
  const [isNarrow, setIsNarrow] = React.useState<boolean | undefined>(undefined)
  const [node, setNode] = React.useState<Element | null>(null)
  const ref = React.useCallback((el: Element | null) => setNode(el), [])

  React.useLayoutEffect(() => {
    if (!node) return

    const update = (width: number) => setIsNarrow(width < breakpoint)

    // Measure synchronously as soon as the node is known — before this
    // commit paints, so a narrow container never renders a visible "plenty
    // of room" frame first.
    update(node.getBoundingClientRect().width)

    if (typeof ResizeObserver === "undefined") return

    const ro = new ResizeObserver((entries) => {
      const entry = entries[0]
      if (!entry) return
      update(entry.contentRect.width)
    })
    ro.observe(node)
    return () => ro.disconnect()
  }, [node, breakpoint])

  return [!!isNarrow, ref] as const
}
