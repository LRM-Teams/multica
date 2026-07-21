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
