/**
 * LRM-281 / LRM-364 / LRM-238: ListAgents hides group managers (LRM-233), so
 * `useActorName` returns honest directory-miss sentinels ("Unknown Agent" /
 * "Unknown"). Callers that need a real display label must treat those as a
 * miss and resolve via GET /member-profiles — never paper over with emit-time
 * names or keep showing the sentinel to users.
 */

export const DIRECTORY_MISS_SENTINELS = new Set(["Unknown Agent", "Unknown"]);

/** True when `getActorName` returned the directory-miss sentinel (or empty). */
export function isDirectoryActorMiss(name: string | null | undefined): boolean {
  const trimmed = name?.trim() ?? "";
  return !trimmed || DIRECTORY_MISS_SENTINELS.has(trimmed);
}

/**
 * Resolve a display name from the live actor directory. Returns null on miss
 * so callers can fall through to member-profile (DB) without inventing copy.
 */
export function directoryActorDisplayName(
  getActorName: (type: string, id: string, fallback?: string) => string,
  type: "agent" | "member",
  id: string,
): string | null {
  // No fallback arg — a miss must not invent a display name.
  const name = getActorName(type, id).trim();
  return isDirectoryActorMiss(name) ? null : name;
}

/** Resolve a display name from a member-profile payload. */
export function profileActorDisplayName(
  profile: { display_name?: string | null; name?: string | null } | null | undefined,
): string | null {
  if (!profile) return null;
  const name = (profile.display_name || profile.name || "").trim();
  return name || null;
}

/** Map reaction/system actor_type onto the identity-cache types. */
export function toDirectoryActorType(type: string | undefined): "agent" | "member" | null {
  if (type === "agent") return "agent";
  if (type === "member" || type === "human" || type === "user") return "member";
  return null;
}

/** member-profile API path segment for a directory actor type. */
export function toMemberProfileType(type: "agent" | "member"): "agent" | "user" {
  return type === "member" ? "user" : "agent";
}
