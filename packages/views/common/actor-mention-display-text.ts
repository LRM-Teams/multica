import { normalizeActorHandle } from "@multica/core/identity";

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function isUuid(value: string): boolean {
  return UUID_RE.test(value);
}

/**
 * LRM-515: main-line ink for a structured agent/member @mention.
 *
 * - Prefer live `display_name` (directory or member-profile).
 * - On miss: gray `@handle` — never paint a bare UUID, never invent a wrong
 *   display name from emit-time slug (LRM-238).
 * - Handle/slug belongs in hover peek, not as branded main ink.
 */
export type ActorMentionInk = {
  /** Visible label without leading `@`. */
  primary: string;
  /** True when we only have a handle / interim slug (muted token). */
  unresolved: boolean;
};

export function resolveActorMentionInk(options: {
  displayName: string | null | undefined;
  handle?: string | null;
  /** Emit-time / span label (often `@slug`); handle fallback only. */
  emitLabel?: string | null;
}): ActorMentionInk | null {
  const display = options.displayName?.trim().replace(/^@+/, "") ?? "";
  // Live identity won (directory or member-profile) → branded main ink.
  if (display && !isUuid(display)) {
    return { primary: display, unresolved: false };
  }

  // Miss: gray @handle only — never bare uuid, never invent a display_name.
  const handle =
    normalizeActorHandle(options.handle) ||
    normalizeActorHandle(options.emitLabel);
  if (handle && !isUuid(handle)) {
    return { primary: handle, unresolved: true };
  }

  return null;
}
