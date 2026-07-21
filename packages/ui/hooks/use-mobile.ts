import * as React from "react"

const MOBILE_BREAKPOINT = 768

export function useIsMobile() {
  return useIsNarrowerThan(MOBILE_BREAKPOINT)
}

/**
 * Generic viewport-width threshold — same matchMedia/listener shape as
 * `useIsMobile`, parameterized for responsive decisions that need a
 * different cutover than the global mobile breakpoint.
 *
 * #568 follow-up: the channel header's action-icon row needs more room than
 * the two-pane desktop layout leaves at MOBILE_BREAKPOINT (768) — the list
 * rail + minimum title width eat into the detail pane, so the icon row
 * doesn't reliably fit until well past 768. That's a layout-fit concern
 * local to the action cluster, not a "switch to single-pane mobile
 * navigation" concern, so it gets its own narrower/wider threshold instead
 * of widening MOBILE_BREAKPOINT (which drives the list↔detail single-column
 * switch, back button, composer sizing, etc. everywhere `useIsMobile` is
 * used — changing it would ripple far beyond the header).
 */
export function useIsNarrowerThan(breakpoint: number) {
  const [isNarrow, setIsNarrow] = React.useState<boolean | undefined>(undefined)

  React.useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${breakpoint - 1}px)`)
    const onChange = () => {
      setIsNarrow(window.innerWidth < breakpoint)
    }
    mql.addEventListener("change", onChange)
    setIsNarrow(window.innerWidth < breakpoint)
    return () => mql.removeEventListener("change", onChange)
  }, [breakpoint])

  return !!isNarrow
}

/**
 * Container-width sibling of `useIsNarrowerThan` — same boolean contract
 * (`width < breakpoint`), but the width comes from a specific element's own
 * rendered box (via `ResizeObserver`), not `window.innerWidth`.
 *
 * #568 follow-up (design/product review blocker): a global viewport
 * breakpoint is the wrong signal for the channel header's action-icon row
 * because the two-pane layout is resizable — the list↔detail pane divider
 * is user-draggable, so viewport width and the detail pane's actual width
 * can diverge in either direction:
 *   - a wide viewport (e.g. 1440px) with the divider dragged so the detail
 *     pane is narrow still needs to collapse, even though viewport width is
 *     nowhere near any global breakpoint;
 *   - a narrower viewport with a wide detail pane (narrow list rail) has
 *     room and shouldn't collapse early.
 * Measuring the element that actually renders the header (or its ancestor,
 * as long as its box tracks the detail pane's width) fixes both directions
 * at once, since it also naturally reacts to a right-side thread/settings
 * panel opening and squeezing the same container further.
 *
 * Deliberately not a CSS `@container` query: the decision here also gates
 * which *subtree* mounts (the compact trigger opens a stateful bottom
 * Drawer reused from the true-mobile path), not just which of two already-
 * mounted nodes is visible — and this codebase's component tests run under
 * jsdom, which doesn't implement container-query evaluation at all, so a
 * pure-CSS version would be unverifiable by anything short of a real
 * browser. A JS boolean driven by `ResizeObserver` keeps the same
 * testable, mockable shape as `useIsNarrowerThan` above.
 *
 * Takes ownership of the ref (returns a callback ref to attach) rather than
 * accepting a caller-supplied `RefObject`: the measured element can mount
 * *after* this hook's first render (e.g. behind another piece of state that
 * flips a tick later), and an object ref gives no signal when `.current`
 * changes — a `useEffect` with a stable dependency array would only ever
 * see the pre-mount `null` and never re-run. A callback ref fires exactly
 * when the DOM node attaches (or is replaced/unmounted), so the internal
 * effect always observes the real node once it exists.
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
