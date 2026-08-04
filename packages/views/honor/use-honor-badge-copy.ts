"use client";

import { useCallback } from "react";
import { useT } from "../i18n";

export const HUMAN_HONOR_BADGE_IDS = [
  "founding",
  "stardust",
  "mercury",
  "venus",
  "earth",
  "mars",
  "jupiter",
  "saturn",
  "uranus",
  "neptune",
  "pluto",
  "veteran",
  "red_dwarf",
  "blue_giant",
  "quasar",
  "builder",
  "collaborator",
  "lunar_spark",
  "comet_trail",
  "asteroid_scout",
  "eclipse_watcher",
  "pulsar_ping",
  "solar_sailor",
  "orbital_cadet",
  "lunar_architect",
  "pathfinder",
  "voyager",
  "beacon_keeper",
  "relay_master",
  "archive_seed",
  "constellation_map",
  "aurora_weaver",
  "galaxy_roamer",
  "wormhole_cartographer",
  "terraformer",
  "foundry_heart",
  "nexus_link",
  "helix_mind",
  "prism_core",
  "plasma_orb",
  "quantum_gate",
  "singularity",
  "celestial_crown",
  "event_horizon",
  "cosmic_tree",
  "infinity_engine",
  "signal_architect",
  "chronicle_engine",
  "steady_light",
  "everpresent",
  "delivery_singularity",
] as const;

export type HumanHonorBadgeId = (typeof HUMAN_HONOR_BADGE_IDS)[number];

type HonorBadgeCopySource = {
  id: string;
  title: string;
  description?: string;
  unlock_rule?: string;
  secret?: boolean;
  unlocked?: boolean;
  progress?: { label: string; current?: number; target?: number };
};

export type HonorBadgeCopy = {
  title: string;
  description: string;
  unlockRule: string;
  progressLabel: string;
};

type BadgeRequirement =
  | { kind: "founding" }
  | { kind: "level"; level: number }
  | {
      kind: "pillar";
      pillar: "usage" | "presence" | "delivery" | "community";
      tier: number;
    };

const humanHonorBadgeIds = new Set<string>(HUMAN_HONOR_BADGE_IDS);

const requirements: Record<HumanHonorBadgeId, BadgeRequirement> = {
  founding: { kind: "founding" },
  stardust: { kind: "level", level: 3 },
  mercury: { kind: "level", level: 5 },
  venus: { kind: "level", level: 8 },
  earth: { kind: "level", level: 10 },
  mars: { kind: "level", level: 12 },
  jupiter: { kind: "level", level: 15 },
  saturn: { kind: "level", level: 18 },
  uranus: { kind: "level", level: 22 },
  neptune: { kind: "level", level: 26 },
  pluto: { kind: "level", level: 30 },
  veteran: { kind: "level", level: 20 },
  red_dwarf: { kind: "level", level: 35 },
  blue_giant: { kind: "level", level: 40 },
  quasar: { kind: "level", level: 50 },
  builder: { kind: "pillar", pillar: "delivery", tier: 4 },
  collaborator: { kind: "pillar", pillar: "community", tier: 3 },
  lunar_spark: { kind: "level", level: 2 },
  comet_trail: { kind: "level", level: 4 },
  asteroid_scout: { kind: "level", level: 6 },
  eclipse_watcher: { kind: "level", level: 7 },
  pulsar_ping: { kind: "level", level: 9 },
  solar_sailor: { kind: "level", level: 11 },
  orbital_cadet: { kind: "level", level: 13 },
  lunar_architect: { kind: "level", level: 14 },
  pathfinder: { kind: "level", level: 16 },
  voyager: { kind: "level", level: 17 },
  beacon_keeper: { kind: "level", level: 19 },
  relay_master: { kind: "level", level: 21 },
  archive_seed: { kind: "level", level: 23 },
  constellation_map: { kind: "level", level: 24 },
  aurora_weaver: { kind: "level", level: 25 },
  galaxy_roamer: { kind: "level", level: 27 },
  wormhole_cartographer: { kind: "level", level: 28 },
  terraformer: { kind: "level", level: 29 },
  foundry_heart: { kind: "level", level: 32 },
  nexus_link: { kind: "level", level: 34 },
  helix_mind: { kind: "level", level: 37 },
  prism_core: { kind: "level", level: 42 },
  plasma_orb: { kind: "level", level: 45 },
  quantum_gate: { kind: "level", level: 48 },
  singularity: { kind: "level", level: 52 },
  celestial_crown: { kind: "level", level: 54 },
  event_horizon: { kind: "level", level: 56 },
  cosmic_tree: { kind: "level", level: 58 },
  infinity_engine: { kind: "level", level: 80 },
  signal_architect: { kind: "pillar", pillar: "usage", tier: 3 },
  chronicle_engine: { kind: "pillar", pillar: "usage", tier: 6 },
  steady_light: { kind: "pillar", pillar: "presence", tier: 4 },
  everpresent: { kind: "pillar", pillar: "presence", tier: 8 },
  delivery_singularity: { kind: "pillar", pillar: "delivery", tier: 8 },
};

export function useHonorBadgeCopy() {
  const { t } = useT("settings");

  const progressLabel = useCallback(
    (label?: string): string => {
      switch (label) {
        case "founding":
          return t(($) => $.honor.badge_progress_founding);
        case "level":
          return t(($) => $.honor.badge_progress_level);
        case "usage":
          return t(($) => $.honor.badge_progress_usage);
        case "presence":
          return t(($) => $.honor.badge_progress_presence);
        case "delivery":
          return t(($) => $.honor.badge_progress_delivery);
        case "community":
          return t(($) => $.honor.badge_progress_community);
        default:
          return label?.trim() ?? "";
      }
    },
    [t],
  );

  const pillarLabel = useCallback(
    (pillar: "usage" | "presence" | "delivery" | "community"): string => {
      switch (pillar) {
        case "usage":
          return t(($) => $.honor.pillar_usage);
        case "presence":
          return t(($) => $.honor.pillar_presence);
        case "delivery":
          return t(($) => $.honor.pillar_delivery);
        case "community":
          return t(($) => $.honor.pillar_community);
      }
    },
    [t],
  );

  return useCallback(
    (badge: HonorBadgeCopySource): HonorBadgeCopy => {
      if (badge.secret && badge.unlocked === false) {
        return {
          title: t(($) => $.honor.secret_badge),
          description: t(($) => $.honor.secret_description),
          unlockRule: "",
          progressLabel: progressLabel(badge.progress?.label),
        };
      }

      if (!humanHonorBadgeIds.has(badge.id)) {
        return {
          title: badge.title,
          description: badge.description?.trim() ?? "",
          unlockRule: badge.unlock_rule?.trim() ?? "",
          progressLabel: progressLabel(badge.progress?.label),
        };
      }

      const id = badge.id as HumanHonorBadgeId;
      const requirement = requirements[id];
      let unlockRule: string;
      if (requirement.kind === "founding") {
        unlockRule = t(($) => $.honor.badge_unlock_founding);
      } else if (requirement.kind === "level") {
        unlockRule = t(($) => $.honor.badge_unlock_level, {
          level: requirement.level,
        });
      } else {
        unlockRule = t(($) => $.honor.badge_unlock_pillar, {
          pillar: pillarLabel(requirement.pillar),
          tier: requirement.tier,
        });
      }

      return {
        title: t(($) => $.honor.badge_copy[id].title),
        description: t(($) => $.honor.badge_copy[id].description),
        unlockRule,
        progressLabel: progressLabel(badge.progress?.label),
      };
    },
    [pillarLabel, progressLabel, t],
  );
}
