import type { Agent } from "@multica/core/types";
import { WINDY_AGENT_NAME } from "../onboarding/templates";

const LEGACY_WINDY_NAMES = new Set([WINDY_AGENT_NAME, "Windy", "Joe"]);

export function isWindyAgent(agent: Agent): boolean {
  return LEGACY_WINDY_NAMES.has(agent.display_name) || LEGACY_WINDY_NAMES.has(agent.name);
}

export function findWindyAgent(agents: readonly Agent[]): Agent | null {
  const candidates = agents.filter(isWindyAgent);
  if (candidates.length === 0) return null;
  return candidates.reduce((best, candidate) =>
    preferWindyAgent(candidate, best) ? candidate : best,
  );
}

function preferWindyAgent(candidate: Agent, current: Agent): boolean {
  if (!!candidate.archived_at !== !!current.archived_at) return !candidate.archived_at;
  if (!!candidate.runtime_id !== !!current.runtime_id) return !!candidate.runtime_id;
  const candidateIsWendy = candidate.display_name === WINDY_AGENT_NAME;
  const currentIsWendy = current.display_name === WINDY_AGENT_NAME;
  if (candidateIsWendy !== currentIsWendy) return candidateIsWendy;
  const candidateTime = candidate.updated_at || candidate.created_at || "";
  const currentTime = current.updated_at || current.created_at || "";
  if (candidateTime !== currentTime) return candidateTime > currentTime;
  return candidate.id < current.id;
}

/**
 * True when the workspace already has a Wendy-compatible agent with a runtime
 * configured — i.e. the account has been through setup. Used to suppress the
 * setup modal so a WINDY_SETUP_VERSION bump doesn't re-block already-configured
 * users (#219). Detection matches the server's Wendy/Windy/Joe migration path.
 */
export function accountHasConfiguredWindy(agents: readonly Agent[]): boolean {
  const windy = findWindyAgent(agents);
  return !!windy && !windy.archived_at && !!windy.runtime_id;
}
