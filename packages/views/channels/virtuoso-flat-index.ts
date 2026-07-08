/**
 * Map a message's index in the `messages` data array to the flat item index
 * react-virtuoso expects for `scrollToIndex` / `initialTopMostItemIndex`.
 *
 * `firstItemIndex` is Virtuoso's stable prepend anchor: prepending older history
 * decreases it so existing rows keep their flat index and the viewport doesn't
 * jump. Every scroll target must therefore be expressed as
 * `firstItemIndex + dataIndex`, never a bare data index. #325 phase-2 block 4
 * routes all scroll targets through this one function so the mapping (and any
 * off-by-one, which silently lands the scroll on the wrong message — the
 * #883-adjacent failure class) lives in exactly one place.
 */
export function toFlatItemIndex(firstItemIndex: number, dataIndex: number): number {
  return firstItemIndex + dataIndex;
}
