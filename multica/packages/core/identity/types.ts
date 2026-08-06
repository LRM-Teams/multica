/**
 * Minimal actor identity fields shared by members and agents.
 *
 * - `display_name` — human-facing primary label (may be empty on older payloads)
 * - `name` — stable unique handle used for routing and @mention syntax
 */
export interface ActorIdentityFields {
  display_name?: string | null;
  name: string;
}

export interface ActorIdentityPresentation {
  /** Primary label shown in UI (display_name → name → fallback). */
  displayName: string;
  /** Normalized handle without a leading `@`. */
  handle: string;
  /** Weak secondary label (`@handle`), or null when absent. */
  handleLabel: string | null;
  /** Whether the secondary @handle should render under the primary label. */
  showHandleLabel: boolean;
}

export interface ActorIdentitySearchOptions {
  /** Extra strings to match (e.g. member email). */
  extra?: string[];
  /**
   * Optional extended matcher for non-Latin scripts (e.g. pinyin).
   * Applied to displayName, handle, and each `extra` field.
   */
  extendedMatch?: (text: string, query: string) => boolean;
}