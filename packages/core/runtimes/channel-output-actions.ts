import type { Agent, AgentRuntime } from "../types";
import { deriveRuntimeHealth } from "./derive-health";

export const CHANNEL_OUTPUT_ACTIONS_CAPABILITY = "channel_output_actions";

export type ChannelOutputRuntimeStatus =
  | "ok"
  | "outdated"
  | "disconnected"
  | "missing";

type RuntimeBinding = Pick<Agent, "runtime_id"> | null | undefined;
type RuntimeCapability =
  | Pick<AgentRuntime, "status" | "last_seen_at" | "capabilities">
  | null
  | undefined;

export function deriveChannelOutputRuntimeStatus(
  agent: RuntimeBinding,
  runtime: RuntimeCapability,
  now: number,
): ChannelOutputRuntimeStatus {
  if (!agent?.runtime_id || !runtime) return "missing";
  // Derived, staleness-aware health instead of the raw `status` column —
  // see #10 ("runtime online status" had two divergent sources across the
  // app). A silently-stopped heartbeat can leave `status: "online"` long
  // after the daemon is actually gone.
  if (deriveRuntimeHealth(runtime, now) !== "online") return "disconnected";
  return runtime.capabilities?.includes(CHANNEL_OUTPUT_ACTIONS_CAPABILITY)
    ? "ok"
    : "outdated";
}

export function isActionableChannelOutputRuntimeStatus(
  status: ChannelOutputRuntimeStatus,
): status is Exclude<ChannelOutputRuntimeStatus, "ok"> {
  return status !== "ok";
}
