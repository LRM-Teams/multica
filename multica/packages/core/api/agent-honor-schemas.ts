import { z } from "zod";
import { agentFleetPillarSchema, agentFleetRankSchema } from "./agent-fleet-schemas";

export const agentAchievementProgressSchema = z.object({
  current: z.number(),
  target: z.number(),
});

export const agentAchievementSchema = z.object({
  id: z.string(),
  title: z.string(),
  description: z.string(),
  svg_key: z.string(),
  category: z.string(),
  xp_reward: z.number(),
  rarity: z.number(),
  secret: z.boolean(),
  unlocked: z.boolean(),
  unlocked_at: z.string().optional(),
  unlock_pct: z.number().optional(),
  progress: agentAchievementProgressSchema.optional(),
});

export const agentAchievementDefinitionSchema = z.object({
  id: z.string(),
  title: z.string(),
  description: z.string(),
  svg_key: z.string(),
  category: z.string(),
  metric: z.string(),
  target: z.number(),
  xp_reward: z.number(),
  rarity: z.number(),
  secret: z.boolean(),
});

const agentHonorMetricsSchema = z.object({
  completed_count: z.number(),
  failed_count: z.number(),
  success_streak: z.number(),
  memory_writes: z.number(),
  evolution_promotions: z.number(),
  distinct_projects: z.number(),
  recovery_count: z.number(),
});

const agentHonorClassThresholdSchema = z.object({
  class_id: z.string(),
  label: z.string(),
  score: z.number(),
});

const agentFleetHistorySchema = z.object({
  fleet_score: z.number(),
  class_id: z.string(),
  class_label: z.string(),
  fleet_rank: z.number(),
  fleet_size: z.number(),
  pillars: agentFleetPillarSchema,
  recorded_at: z.string(),
});

const agentHonorEventSchema = z.object({
  id: z.string(),
  event_type: z.string(),
  source_ref: z.string(),
  xp_delta: z.number(),
  reason: z.string(),
  created_at: z.string(),
});

export const agentHonorRulesSchema = z.object({
  version: z.string(),
  completion_xp: z.number(),
  fleet_window_days: z.number(),
  fleet_min_sample_tasks: z.number(),
  fleet_weights: z.record(z.string(), z.number()),
  fleet_classes: z.array(agentHonorClassThresholdSchema),
  achievement_targets: z.record(z.string(), z.number()),
  achievement_enabled: z.record(z.string(), z.boolean()),
  changelog: z.array(z.string()),
});

export const agentHonorRulesViewSchema = z.object({
  revision: z.number(),
  rules: agentHonorRulesSchema,
  achievements: z.array(agentAchievementDefinitionSchema),
});

export const agentHonorDashboardSchema = z.object({
  agent_id: z.string(),
  level: z.number(),
  total_xp: z.number(),
  xp_to_next_level: z.number(),
  equipped_achievement_id: z.string().optional(),
  showcase_achievement_ids: z.array(z.string()),
  metrics: agentHonorMetricsSchema,
  fleet: agentFleetRankSchema,
  next_fleet_class: agentHonorClassThresholdSchema.optional(),
  achievements: z.array(agentAchievementSchema),
  recent_events: z.array(agentHonorEventSchema),
  fleet_history: z.array(agentFleetHistorySchema),
  rules_version: z.string(),
});

export const agentHonorAdminAuditSchema = z.object({
  id: z.string(),
  agent_id: z.string().optional(),
  action: z.string(),
  details: z.record(z.string(), z.unknown()),
  created_by: z.string(),
  created_at: z.string(),
});

export const agentHonorAdminAuditListSchema = z.array(agentHonorAdminAuditSchema);

export const EMPTY_AGENT_HONOR_DASHBOARD = {
  agent_id: "",
  level: 1,
  total_xp: 0,
  xp_to_next_level: 25,
  showcase_achievement_ids: [],
  metrics: {
    completed_count: 0,
    failed_count: 0,
    success_streak: 0,
    memory_writes: 0,
    evolution_promotions: 0,
    distinct_projects: 0,
    recovery_count: 0,
  },
  fleet: {
    agent_id: "",
    fleet_score: 0,
    class_id: "reserve",
    class_label: "Reserve",
    fleet_rank: 0,
    fleet_size: 0,
    sample_tasks: 0,
    sample_sufficient: false,
    frozen: false,
    pillars: { delivery: 0, evolution: 0, growth: 0, efficiency: 0 },
  },
  achievements: [],
  recent_events: [],
  fleet_history: [],
  rules_version: "",
} satisfies z.infer<typeof agentHonorDashboardSchema>;

export const EMPTY_AGENT_HONOR_RULES_VIEW = {
  revision: 0,
  rules: {
    version: "",
    completion_xp: 10,
    fleet_window_days: 30,
    fleet_min_sample_tasks: 5,
    fleet_weights: {},
    fleet_classes: [],
    achievement_targets: {},
    achievement_enabled: {},
    changelog: [],
  },
  achievements: [],
} satisfies z.infer<typeof agentHonorRulesViewSchema>;
