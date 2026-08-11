import type { RuntimeDevice } from "@multica/core/types";

/**
 * Whether the viewer may bind agents to this runtime.
 * Others' private runtimes are unusable; missing visibility fails closed as private.
 */
export function isRuntimeUsableForUser(
  r: RuntimeDevice,
  currentUserId: string | null,
): boolean {
  if (!currentUserId) return true;
  if (r.owner_id === currentUserId) return true;
  return r.visibility === "public";
}
