import { z } from "zod";

export const honorBadgeSchema = z.object({
  id: z.string(),
  title: z.string(),
  description: z.string(),
  svg_key: z.string(),
});

const honorBadgeProgressSchema = z.object({
  current: z.number(),
  target: z.number(),
  label: z.string(),
});

export const honorBadgeCatalogItemSchema = z.object({
  id: z.string(),
  title: z.string(),
  description: z.string(),
  svg_key: z.string(),
  rarity: z.number(),
  unlock_rule: z.string(),
  secret: z.boolean(),
  unlocked: z.boolean(),
  unlocked_at: z.string().optional(),
  unlock_pct: z.number().optional(),
  progress: honorBadgeProgressSchema.optional(),
});

export const honorRecentUnlockSchema = z.object({
  id: z.string(),
  title: z.string(),
  description: z.string(),
  svg_key: z.string(),
  unlocked_at: z.string(),
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
  equipped_badge_manual: z.boolean().optional(),
  showcase_badge_ids: z.array(z.string()).optional(),
  badges_unlocked: z.number().optional(),
  badges_total: z.number().optional(),
  badge_catalog: z.array(honorBadgeCatalogItemSchema).optional(),
  recent_unlocks: z.array(honorRecentUnlockSchema).optional(),
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
  showcase_badges: z.array(honorBadgeSchema).optional(),
  recent_unlocks: z.array(honorRecentUnlockSchema).optional(),
  badges_unlocked: z.number().optional(),
  badges_total: z.number().optional(),
  unlocked_badges: z.array(honorBadgeSchema),
});

export const honorCompareSchema = z.object({
  self: z.object({
    user_id: z.string(),
    level: z.number(),
    unlocked_count: z.number(),
    total_badges: z.number(),
  }),
  other: z.object({
    user_id: z.string(),
    level: z.number(),
    unlocked_count: z.number(),
    total_badges: z.number(),
  }),
  shared_badges: z.array(honorBadgeSchema),
  self_only_badges: z.array(honorBadgeSchema),
  other_only_badges: z.array(honorBadgeSchema),
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

export const honorBadgeUnlockedPayloadSchema = z.object({
  user_id: z.string(),
  badge: honorBadgeSchema,
  unlock_pct: z.number().optional(),
});
