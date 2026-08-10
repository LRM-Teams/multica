export type {
  AgentPresence,
  AgentPresenceItem,
  AgentPresenceResponse,
} from "../types";

export type Workload = "working" | "queued" | "idle";

export interface AgentWorkloadDetail {
  workload: Workload;
  runningCount: number;
  queuedCount: number;
}
