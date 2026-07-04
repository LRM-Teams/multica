import type { Agent } from "@multica/core/types";
import { WINDY_AGENT_NAME } from "../onboarding/templates";

/**
 * True when the workspace already has a Wendy agent with a runtime configured —
 * i.e. the account has been through Wendy setup. Used to suppress the setup
 * modal so a WINDY_SETUP_VERSION bump doesn't re-block already-configured users
 * (#219). Detection is intentionally lenient (name + runtime present); the
 * modal's "Later" dismiss is the safety net for anything this misses.
 */
export function accountHasConfiguredWindy(agents: readonly Agent[]): boolean {
  return agents.some((a) => a.display_name === WINDY_AGENT_NAME && !!a.runtime_id);
}
