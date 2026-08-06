import type { AgentFleetRank, AgentFleetPillarScores } from "./agent-fleet";

export interface AgentHonorMetrics {
  completed_count: number;
  failed_count: number;
  success_streak: number;
  memory_writes: number;
  evolution_promotions: number;
  distinct_projects: number;
  recovery_count: number;
}

export interface AgentAchievementProgress {
  current: number;
  target: number;
}

export interface AgentAchievement {
  id: string;
  title: string;
  description: string;
  svg_key: string;
  category: string;
  xp_reward: number;
  rarity: number;
  secret: boolean;
  unlocked: boolean;
  unlocked_at?: string;
  unlock_pct?: number;
  progress?: AgentAchievementProgress;
}

export interface AgentAchievementDefinition {
  id: string;
  title: string;
  description: string;
  svg_key: string;
  category: string;
  metric: string;
  target: number;
  xp_reward: number;
  rarity: number;
  secret: boolean;
}

export interface AgentHonorEvent {
  id: string;
  event_type: string;
  source_ref: string;
  xp_delta: number;
  reason: string;
  created_at: string;
}

export interface AgentFleetHistory {
  fleet_score: number;
  class_id: string;
  class_label: string;
  fleet_rank: number;
  fleet_size: number;
  pillars: AgentFleetPillarScores;
  recorded_at: string;
}

export interface AgentHonorClassThreshold {
  class_id: string;
  label: string;
  score: number;
}

export interface AgentHonorRules {
  version: string;
  completion_xp: number;
  fleet_window_days: number;
  fleet_min_sample_tasks: number;
  fleet_weights: Record<string, number>;
  fleet_classes: AgentHonorClassThreshold[];
  achievement_targets: Record<string, number>;
  achievement_enabled: Record<string, boolean>;
  changelog: string[];
}

export interface AgentHonorRulesView {
  revision: number;
  rules: AgentHonorRules;
  achievements: AgentAchievementDefinition[];
}

export interface AgentHonorDashboard {
  agent_id: string;
  level: number;
  total_xp: number;
  xp_to_next_level: number;
  equipped_achievement_id?: string;
  showcase_achievement_ids: string[];
  metrics: AgentHonorMetrics;
  fleet: AgentFleetRank;
  next_fleet_class?: AgentHonorClassThreshold;
  achievements: AgentAchievement[];
  recent_events: AgentHonorEvent[];
  fleet_history: AgentFleetHistory[];
  rules_version: string;
}

export interface AgentHonorAdminAudit {
  id: string;
  agent_id?: string;
  action: string;
  details: Record<string, unknown>;
  created_by: string;
  created_at: string;
}

export interface UpdateAgentHonorShowcaseRequest {
  achievement_ids: string[];
  equipped_id: string;
}

export type AgentHonorGrantRequest =
  | { kind: "xp"; xp: number; reason: string; grant_id?: string }
  | { kind: "achievement"; achievement_id: string; reason: string };
