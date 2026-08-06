import { normalizeActorHandle } from "./display";
import type { ActorIdentitySearchOptions } from "./types";

/** Normalize a user-typed actor search query (trim, lowercase, strip leading `@`). */
export function normalizeActorSearchQuery(query: string): string {
  return query.trim().replace(/^@+/, "").toLowerCase();
}

function matchesText(
  text: string,
  query: string,
  extendedMatch?: ActorIdentitySearchOptions["extendedMatch"],
): boolean {
  const normalized = text.toLowerCase();
  if (normalized.includes(query)) return true;
  return extendedMatch?.(text, query) ?? false;
}

/**
 * Whether an actor matches a search query against display name, handle,
 * and optional extra fields (e.g. email).
 */
export function matchesActorIdentitySearch(
  displayName: string,
  handle: string,
  query: string,
  options: ActorIdentitySearchOptions = {},
): boolean {
  const q = normalizeActorSearchQuery(query);
  if (!q) return true;

  const normalizedHandle = normalizeActorHandle(handle);
  const { extra = [], extendedMatch } = options;

  if (matchesText(displayName, q, extendedMatch)) return true;
  if (normalizedHandle && matchesText(normalizedHandle, q, extendedMatch)) return true;

  for (const field of extra) {
    if (field && matchesText(field, q, extendedMatch)) return true;
  }

  return false;
}

/**
 * Rank how well a handle matches a query for sorting picker results.
 * Lower is better: 0 = exact, 1 = prefix, 2 = substring, 3 = no handle match.
 */
export function actorHandleSearchRank(handle: string, query: string): number {
  const q = normalizeActorSearchQuery(query);
  if (!q) return 3;

  const normalizedHandle = normalizeActorHandle(handle).toLowerCase();
  if (!normalizedHandle) return 3;
  if (normalizedHandle === q) return 0;
  if (normalizedHandle.startsWith(q)) return 1;
  if (normalizedHandle.includes(q)) return 2;
  return 3;
}