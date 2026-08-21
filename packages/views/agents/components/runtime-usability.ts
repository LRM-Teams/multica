import type { RuntimeDevice } from "@multica/core/types";

/**
 * Whether the viewer may bind agents to this runtime.
 *
 * `bindableIds` is the server's answer (GET /api/computers carries the
 * bindable runtimes per Computer) and wins whenever it is present — the rule
 * belongs on the server, next to the visibility it enforces. The local
 * fallback below only runs against a server that does not send the field: it
 * reads `owner_id`, which is the *machine's* owner projected onto the runtime
 * row, so it cannot survive as the long-term rule.
 */
export function isRuntimeUsableForUser(
  r: RuntimeDevice,
  currentUserId: string | null,
  bindableIds?: ReadonlySet<string> | null,
): boolean {
  if (bindableIds) return bindableIds.has(r.id);
  if (!currentUserId) return true;
  if (r.owner_id === currentUserId) return true;
  return r.visibility === "public";
}
