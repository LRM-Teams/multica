import { z } from "zod";

export const fleetClassIdSchema = z.string();

export const agentFleetPillarSchema = z.object({
  delivery: z.number(),
  evolution: z.number(),
  growth: z.number(),
  efficiency: z.number(),
});

export const agentFleetRankSchema = z.object({
  agent_id: z.string(),
  fleet_score: z.number(),
  class_id: fleetClassIdSchema,
  class_label: z.string(),
  fleet_rank: z.number(),
  fleet_size: z.number(),
  sample_tasks: z.number(),
  sample_sufficient: z.boolean(),
  frozen: z.boolean(),
  pillars: agentFleetPillarSchema,
});

export const agentFleetRankListSchema = z.array(agentFleetRankSchema);

export const EMPTY_AGENT_FLEET_RANK_LIST: z.infer<typeof agentFleetRankListSchema> = [];

export const agentFleetRulesSchema = z.object({
  version: z.string(),
  window_days: z.number(),
  min_sample_tasks: z.number(),
  pillar_weights: z.record(z.string(), z.number()),
  class_thresholds: z.array(
    z.object({
      class_id: z.string(),
      min_score: z.number(),
      label: z.string(),
      svg_key: z.string(),
    }),
  ),
  changelog: z.array(z.string()),
});
