export type HonorNameStyleKey =
  | "default"
  | "ice"
  | "member"
  | "emerald"
  | "sapphire"
  | "gold"
  | "coral"
  | "amethyst"
  | "founding"
  | "prismatic"
  | "aurora"
  | "glow"
  | "solar"
  | "shimmer"
  | "nebula"
  | "cyber"
  | "animated_prismatic"
  | "plasma"
  | "animated_glow"
  | "eclipse"
  | "nova"
  | "quantum"
  | "celestial"
  | "mythic"
  | "transcendent";

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

export interface HonorBadgeProgress {
  current: number;
  target: number;
  label: string;
}

export interface HonorBadgeCatalogItem {
  id: string;
  title: string;
  description: string;
  svg_key: string;
  rarity: number;
  unlock_rule: string;
  secret: boolean;
  unlocked: boolean;
  unlocked_at?: string;
  unlock_pct?: number;
  progress?: HonorBadgeProgress;
}

export interface HonorRecentUnlock {
  id: string;
  title: string;
  description: string;
  svg_key: string;
  unlocked_at: string;
}

export interface HonorDashboard {
  level: number;
  total_xp: number;
  xp_to_next_level: number;
  name_style: HonorNameStyleKey | string;
  equipped_badge_id: string | null;
  equipped_badge_manual?: boolean;
  showcase_badge_ids?: string[];
  badges_unlocked?: number;
  badges_total?: number;
  badge_catalog?: HonorBadgeCatalogItem[];
  recent_unlocks?: HonorRecentUnlock[];
  pillars: HonorPillarProgress[];
  unlocked_badges: HonorBadge[];
  unlocked_styles: string[];
  recent_xp: HonorXPEvent[];
}

export interface HonorPublicWall {
  level: number;
  name_style: HonorNameStyleKey | string;
  equipped_badge?: HonorBadge;
  showcase_badges?: HonorBadge[];
  recent_unlocks?: HonorRecentUnlock[];
  badges_unlocked?: number;
  badges_total?: number;
  unlocked_badges: HonorBadge[];
}

export interface HonorCompareSide {
  user_id: string;
  level: number;
  unlocked_count: number;
  total_badges: number;
}

export interface HonorCompareResult {
  self: HonorCompareSide;
  other: HonorCompareSide;
  shared_badges: HonorBadge[];
  self_only_badges: HonorBadge[];
  other_only_badges: HonorBadge[];
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

export interface HonorBadgeUnlockedPayload {
  user_id: string;
  badge: HonorBadge;
  unlock_pct?: number;
}
