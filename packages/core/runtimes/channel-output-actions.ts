import type { Agent, AgentRuntime } from "../types";

export const CHANNEL_OUTPUT_ACTIONS_CAPABILITY = "channel_output_actions";

export type ChannelOutputRuntimeStatus =
  | "ok"
  | "outdated"
  | "disconnected"
  | "missing";

type RuntimeBinding = Pick<Agent, "runtime_id"> | null | undefined;
type RuntimeCapability = Pick<AgentRuntime, "status" | "capabilities"> | null | undefined;

export function deriveChannelOutputRuntimeStatus(
  agent: RuntimeBinding,
  runtime: RuntimeCapability,
): ChannelOutputRuntimeStatus {
  if (!agent?.runtime_id || !runtime) return "missing";
  if (runtime.status !== "online") return "disconnected";
  return runtime.capabilities?.includes(CHANNEL_OUTPUT_ACTIONS_CAPABILITY)
    ? "ok"
    : "outdated";
}

export function isActionableChannelOutputRuntimeStatus(
  status: ChannelOutputRuntimeStatus,
): status is Exclude<ChannelOutputRuntimeStatus, "ok"> {
  return status !== "ok";
}
