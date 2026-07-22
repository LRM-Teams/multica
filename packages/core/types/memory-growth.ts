/**
 * LRM-274 / LRM-303 Memory growth payload on agent profile & member profile.
 * Omitted when the agent has zero valid Phase① memory writes.
 */

export type MemoryGrowthTierId = "bronze" | "silver" | "gold" | "platinum";

export type MemoryGrowthSegmentStatus = "complete" | "current" | "upcoming";

export interface MemoryGrowthSegment {
  tier: MemoryGrowthTierId;
  tier_label: string;
  status: MemoryGrowthSegmentStatus;
}

/** Fine-grained "Next · n/m writes" row toward the next tier threshold. */
export interface MemoryGrowthNextProgress {
  tier: MemoryGrowthTierId;
  tier_label: string;
  current: number;
  required: number;
}

export interface AgentMemoryGrowth {
  total_writes: number;
  tier: MemoryGrowthTierId;
  tier_label: string;
  segments: MemoryGrowthSegment[];
  /** Absent at max tier (all thresholds met). */
  next?: MemoryGrowthNextProgress | null;
}
