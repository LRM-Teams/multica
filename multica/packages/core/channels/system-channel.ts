import type { Channel } from "../types";

/**
 * #642 — the single, capability-level gate every "can this channel be
 * mutated?" affordance must route through: archive, rename (no live entry
 * point yet, but this predicate is where its future gate belongs too),
 * delete, member add/remove, project/Lark link edits, the Settings entry
 * point itself. An unknown/absent `system_key` degrades to a normal
 * channel — this must never be the thing that decides safety; the
 * server-owned invariant is.
 */
export function isImmutableSystemChannel(channel: Pick<Channel, "system_key">): boolean {
  return channel.system_key === "general";
}
