import { AGENT_AVATAR_PRESETS } from "@multica/core/workspace/avatar-url";
import type { AgentAvatarSelection } from "@multica/core/types";

/** Pick a concrete preset path from the shared 24-face pool. */
export function randomAgentAvatarPresetUrl(
  random: () => number = Math.random,
): string {
  const index = Math.floor(random() * AGENT_AVATAR_PRESETS.length);
  return AGENT_AVATAR_PRESETS[index] ?? AGENT_AVATAR_PRESETS[0]!;
}

/** When the user clears a draft-seeded avatar, override the draft with a
 * random picked preset so create does not re-apply draft.avatar_url. */
export function randomPickedAvatarSelection(
  random: () => number = Math.random,
): AgentAvatarSelection {
  return { kind: "picked", preset_url: randomAgentAvatarPresetUrl(random) };
}
