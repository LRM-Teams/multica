import type { Agent } from "@multica/core/types";
import { WINDY_AGENT_NAME } from "../onboarding/templates";

const LEGACY_WINDY_NAMES = new Set([WINDY_AGENT_NAME, "Windy", "Joe"]);

export function isWindyAgent(agent: Agent): boolean {
  return LEGACY_WINDY_NAMES.has(agent.display_name) || LEGACY_WINDY_NAMES.has(agent.name);
}

export function findWindyAgent(agents: readonly Agent[]): Agent | null {
  return agents.find(isWindyAgent) ?? null;
}

/**
 * True when the workspace already has a Wendy-compatible agent with a runtime
 * configured — i.e. the account has been through setup. Used to suppress the
 * setup modal so a WINDY_SETUP_VERSION bump doesn't re-block already-configured
 * users (#219). Detection matches the server's Wendy/Windy/Joe migration path.
 */
export function accountHasConfiguredWindy(agents: readonly Agent[]): boolean {
  return agents.some((a) => isWindyAgent(a) && !!a.runtime_id);
}
