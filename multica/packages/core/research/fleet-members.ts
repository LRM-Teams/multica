import type { ResearchFleetMember } from "../types/research";

/**
 * Keep one member per role (prefer non-archived, lead, then earliest id).
 * Guards UI against concurrent seed races that minted duplicate roles.
 */
export function dedupeResearchFleetMembers(
  members: ResearchFleetMember[],
): ResearchFleetMember[] {
  const byRole = new Map<string, ResearchFleetMember>();
  for (const m of members) {
    if (m.status === "archived") continue;
    const role = m.role || m.id;
    const prev = byRole.get(role);
    if (!prev) {
      byRole.set(role, m);
      continue;
    }
    const preferCurrent =
      (m.is_lead && !prev.is_lead) ||
      (!m.is_lead && !prev.is_lead && m.id < prev.id);
    if (preferCurrent) byRole.set(role, m);
  }
  return [...byRole.values()];
}
