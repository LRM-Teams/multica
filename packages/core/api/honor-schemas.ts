import { z } from "zod";

export const honorBadgeSchema = z.object({
  id: z.string(),
  title: z.string(),
  description: z.string(),
  svg_key: z.string(),
});

export const honorSnapshotSchema = z.object({
  level: z.number(),
  name_style: z.string(),
  equipped_badge: honorBadgeSchema.optional(),
});

export const honorDashboardSchema = z.object({
  level: z.number(),
  total_xp: z.number(),
  xp_to_next_level: z.number(),
  name_style: z.string(),
  equipped_badge_id: z.string().nullable(),
  pillars: z.array(
    z.object({
      pillar: z.string(),
      counter_value: z.number(),
      tier: z.number(),
      next_tier_at: z.number().optional(),
    }),
  ),
  unlocked_badges: z.array(honorBadgeSchema),
  unlocked_styles: z.array(z.string()),
  recent_xp: z.array(
    z.object({
      pillar: z.string(),
      action_type: z.string(),
      xp_delta: z.number(),
      ref_id: z.string().optional(),
      created_at: z.string(),
    }),
  ),
});

export const honorPublicWallSchema = z.object({
  level: z.number(),
  name_style: z.string(),
  equipped_badge: honorBadgeSchema.optional(),
  unlocked_badges: z.array(honorBadgeSchema),
});

export const honorRulesSchema = z.object({
  version: z.string(),
  founding_cutoff: z.string(),
  level_thresholds: z.array(z.object({ level: z.number(), total_xp: z.number() })),
  pillar_tier_tables: z.record(z.string(), z.array(z.number())),
  action_rules: z.record(
    z.string(),
    z.object({ pillar: z.string(), xp_delta: z.number(), daily_cap: z.number() }),
  ),
  name_style_unlocks: z.array(z.object({ id: z.string(), min_level: z.number() })),
  badge_catalog: z.array(honorBadgeSchema.extend({ rarity: z.number() })),
  changelog: z.array(z.string()),
});
