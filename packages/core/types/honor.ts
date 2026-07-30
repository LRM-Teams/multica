export type HonorNameStyleKey =
  | "default"
  | "member"
  | "gold"
  | "founding"
  | "prismatic"
  | "glow"
  | "shimmer"
  | "animated_prismatic"
  | "animated_glow";

export interface HonorBadge {
  id: string;
  title: string;
  description: string;
  svg_key: string;
}

export interface HonorSnapshot {
  level: number;
  name_style: HonorNameStyleKey | string;
  equipped_badge?: HonorBadge;
}

export interface HonorPillarProgress {
  pillar: string;
  counter_value: number;
  tier: number;
  next_tier_at?: number;
}

export interface HonorXPEvent {
  pillar: string;
  action_type: string;
  xp_delta: number;
  ref_id?: string;
  created_at: string;
}

export interface HonorDashboard {
  level: number;
  total_xp: number;
  xp_to_next_level: number;
  name_style: HonorNameStyleKey | string;
  equipped_badge_id: string | null;
  pillars: HonorPillarProgress[];
  unlocked_badges: HonorBadge[];
  unlocked_styles: string[];
  recent_xp: HonorXPEvent[];
}

export interface HonorPublicWall {
  level: number;
  name_style: HonorNameStyleKey | string;
  equipped_badge?: HonorBadge;
  unlocked_badges: HonorBadge[];
}

export interface HonorRulesDocument {
  version: string;
  founding_cutoff: string;
  level_thresholds: Array<{ level: number; total_xp: number }>;
  pillar_tier_tables: Record<string, number[]>;
  action_rules: Record<string, { pillar: string; xp_delta: number; daily_cap: number }>;
  name_style_unlocks: Array<{ id: string; min_level: number }>;
  badge_catalog: Array<HonorBadge & { rarity: number }>;
  changelog: string[];
}
