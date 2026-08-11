/**
 * Members Directory selection in the URL query (ADR 0013 / #2773).
 *
 * Shape: `?member=agent:<uuid>` | `?member=user:<uuid>`
 */

export type MembersSelectionKind = "agent" | "user";

export type MembersSelection = {
  kind: MembersSelectionKind;
  id: string;
};

const MEMBER_QUERY_KEY = "member";

/** Query param name used on the Members Directory route. */
export function membersSelectionQueryKey(): string {
  return MEMBER_QUERY_KEY;
}

/**
 * Encode a directory selection for the Members route query string
 * (without leading `?`). Returns empty string when id is blank.
 */
export function encodeMembersSelection(
  kind: MembersSelectionKind,
  id: string,
): string {
  const trimmed = id.trim();
  if (!trimmed) return "";
  return `${MEMBER_QUERY_KEY}=${encodeURIComponent(`${kind}:${trimmed}`)}`;
}

/**
 * Parse `member` query value (raw, not full search string).
 * Accepts `agent:<id>` / `user:<id>`. Invalid or empty → null.
 */
export function parseMembersSelectionParam(
  raw: string | null | undefined,
): MembersSelection | null {
  if (raw == null) return null;
  const value = raw.trim();
  if (!value) return null;
  const colon = value.indexOf(":");
  if (colon <= 0) return null;
  const kind = value.slice(0, colon);
  const id = value.slice(colon + 1).trim();
  if (!id) return null;
  if (kind !== "agent" && kind !== "user") return null;
  return { kind, id };
}

/**
 * Read selection from a URLSearchParams-like object or Record.
 */
export function parseMembersSelectionFromSearch(
  search:
    | { get(name: string): string | null }
    | Record<string, string | string[] | undefined>
    | string
    | null
    | undefined,
): MembersSelection | null {
  if (search == null || search === "") return null;
  if (typeof search === "string") {
    const q = search.startsWith("?") ? search.slice(1) : search;
    const params = new URLSearchParams(q);
    return parseMembersSelectionParam(params.get(MEMBER_QUERY_KEY));
  }
  if (typeof (search as { get?: unknown }).get === "function") {
    return parseMembersSelectionParam(
      (search as { get(name: string): string | null }).get(MEMBER_QUERY_KEY),
    );
  }
  const rec = search as Record<string, string | string[] | undefined>;
  const v = rec[MEMBER_QUERY_KEY];
  const raw = Array.isArray(v) ? v[0] : v;
  return parseMembersSelectionParam(raw ?? null);
}

/** Append or replace selection on a Members base path. */
export function membersPathWithSelection(
  membersBasePath: string,
  kind: MembersSelectionKind,
  id: string,
): string {
  const q = encodeMembersSelection(kind, id);
  if (!q) return membersBasePath;
  // Rebuild from path-only so callers may pass a base that already had query.
  const base = membersBasePath.split("?")[0] ?? membersBasePath;
  return `${base}?${q}`;
}
