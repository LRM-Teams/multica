import * as React from "react";

// The channel Tasks board is board-local narrow (#685 closure): with side
// panels open the board's main area is only ~250px at a 768px viewport, so the
// horizontal-column layout is badly cramped at EXACTLY 768. It therefore
// switches to the segmented single-column layout at ≤768px — one px wider than
// the app-wide `useIsMobile` (< 768). This is deliberately NOT the shared hook:
// `useIsMobile`'s threshold is used app-wide and must not move for one board.
const NARROW_BREAKPOINT = 768;

/**
 * Board-local `≤768px` viewport check for the channel Tasks board. SSR-safe:
 * defaults to `false` (undefined) until the mount effect measures the real
 * viewport, and re-measures on viewport change.
 */
export function useIsNarrow() {
  const [isNarrow, setIsNarrow] = React.useState<boolean | undefined>(undefined);

  React.useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${NARROW_BREAKPOINT}px)`);
    const onChange = () => setIsNarrow(window.innerWidth <= NARROW_BREAKPOINT);
    mql.addEventListener("change", onChange);
    onChange();
    return () => mql.removeEventListener("change", onChange);
  }, []);

  return !!isNarrow;
}
