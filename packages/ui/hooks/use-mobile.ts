import * as React from "react"

const MOBILE_BREAKPOINT = 768

export function useIsMobile() {
  const [isMobile, setIsMobile] = React.useState<boolean | undefined>(undefined)

  React.useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
    const onChange = () => {
      setIsMobile(window.innerWidth < MOBILE_BREAKPOINT)
    }
    mql.addEventListener("change", onChange)
    setIsMobile(window.innerWidth < MOBILE_BREAKPOINT)
    return () => mql.removeEventListener("change", onChange)
  }, [])

  return !!isMobile
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
 */
export function useContainerNarrowerThan(breakpoint: number) {
  const [isNarrow, setIsNarrow] = React.useState<boolean | undefined>(undefined)
  const [node, setNode] = React.useState<Element | null>(null)
  const ref = React.useCallback((el: Element | null) => setNode(el), [])

  React.useEffect(() => {
    if (!node) return

    const update = (width: number) => setIsNarrow(width < breakpoint)

    // Measure synchronously as soon as the node is known — don't wait for
    // the first ResizeObserver callback, or the "plenty of room" branch
    // renders for one tick even on a narrow container.
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
