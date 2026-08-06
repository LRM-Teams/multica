"use client";

import { useActorName } from "@multica/core/workspace/hooks";
import { useResolvedActorIdentity } from "./use-resolved-actor-identity";

/** Strip a leading `@` from a structured-mention label / span substring. */
export function stripMentionAtPrefix(label?: string | null): string | undefined {
  if (!label) return undefined;
  const trimmed = label.replace(/^@+/, "").trim();
  return trimmed || undefined;
}

export type ActorMentionChipLabel = {
  /** Visible chip text without the leading `@`. */
  name: string;
  /**
   * True when we could not resolve a live display_name yet (pending profile)
   * or on hard miss — callers should render muted / gray ink and must not
   * show a raw UUID or "Unknown Agent" (LRM-515 / LRM-238).
   */
  unresolved: boolean;
  /** Routing handle for peek/hover when primary ink is display_name. */
  handlePeek: string | undefined;
};

/**
 * LRM-515: render-time mention chip label.
 *
 * Structured message parts store the authored `@handle` span as `label`.
 * ListAgents also hides group managers / channel-only agents (LRM-233), so
 * `useActorName(id, handleFallback)` silently paints the slug. Resolve
 * display_name via {@link useResolvedActorIdentity} (directory → member-profiles)
 * the same way LRM-391 fixed author chrome.
 */
export function useActorMentionChipLabel(
  type: string,
  id: string,
  label?: string | null,
): ActorMentionChipLabel {
  const handle = stripMentionAtPrefix(label);
  const personType = type === "agent" || type === "member" ? type : null;
  const identity = useResolvedActorIdentity(personType ? id : undefined, personType);
  const { getActorName } = useActorName();

  if (type === "all") {
    return { name: "all", unresolved: false, handlePeek: undefined };
  }

  if (personType) {
    if (identity.displayName) {
      return {
        name: identity.displayName,
        unresolved: false,
        handlePeek: handle && handle !== identity.displayName ? handle : undefined,
      };
    }
    // Pending or hard miss: keep authored handle (or ellipsis — never UUID).
    return {
      name: handle ?? "…",
      unresolved: true,
      handlePeek: handle,
    };
  }

  return {
    name: getActorName(type, id, handle),
    unresolved: false,
    handlePeek: handle,
  };
}
