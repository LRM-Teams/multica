/**
 * #838 — stable identity for "where an unsent recording was going".
 *
 * Lives outside the component file for two reasons: exporting a non-component
 * from one breaks Fast Refresh (react-doctor `only-export-components`), and the
 * key is used by the page's send handlers, not only by the item that renders it.
 *
 * The pending record lives on the whole page, which outlives the current
 * channel — so it must be bound to an IMMUTABLE target, or a failure in one
 * channel surfaces (and retries) in another.
 */
export function voiceTargetId(channelId: string, threadRootId?: string): string {
  return threadRootId ? `${channelId}:${threadRootId}` : channelId;
}
