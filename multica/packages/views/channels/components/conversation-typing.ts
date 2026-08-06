/**
 * Whether a typing actor should surface in the conversation activity strip.
 *
 * Anchor 7 / A8: an offline or working agent surfaces via the Run / working
 * indicator (queue → wake), NEVER as a transient "typing" indicator — showing
 * an agent as "typing" would be a fabricated presence signal. Only human (and
 * lark / system) actors produce a real typing pulse.
 */
export function isTypingActorVisible(actorType: string): boolean {
  return actorType !== "agent";
}
