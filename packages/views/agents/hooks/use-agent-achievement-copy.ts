"use client";

import { useT } from "../../i18n";

type AgentAchievementCopySource = {
  id: string;
  title: string;
  description?: string;
  category?: string;
};

export type AgentAchievementCopy = {
  title: string;
  description: string;
  category: string;
};

export function useAgentAchievementCategoryName() {
  const { t } = useT("agents");

  return (category?: string): string => {
    switch (category) {
      case "delivery":
        return t(($) => $.honor_agent.achievement_categories.delivery);
      case "reliability":
        return t(($) => $.honor_agent.achievement_categories.reliability);
      case "growth":
        return t(($) => $.honor_agent.achievement_categories.growth);
      case "evolution":
        return t(($) => $.honor_agent.achievement_categories.evolution);
      case "mastery":
        return t(($) => $.honor_agent.achievement_categories.mastery);
      case "fleet":
        return t(($) => $.honor_agent.achievement_categories.fleet);
      default:
        return category?.trim() ?? "";
    }
  };
}

export function useAgentAchievementCopy() {
  const { t } = useT("agents");
  const categoryName = useAgentAchievementCategoryName();

  return (achievement: AgentAchievementCopySource): AgentAchievementCopy => {
    const fallback = {
      title: achievement.title,
      description: achievement.description?.trim() ?? "",
      category: categoryName(achievement.category),
    };

    switch (achievement.id) {
      case "first_launch":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.first_launch.title),
          description: t(($) => $.honor_agent.achievements.first_launch.description),
        };
      case "proven_crew":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.proven_crew.title),
          description: t(($) => $.honor_agent.achievements.proven_crew.description),
        };
      case "veteran_core":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.veteran_core.title),
          description: t(($) => $.honor_agent.achievements.veteran_core.description),
        };
      case "centurion":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.centurion.title),
          description: t(($) => $.honor_agent.achievements.centurion.description),
        };
      case "streak_5":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.streak_5.title),
          description: t(($) => $.honor_agent.achievements.streak_5.description),
        };
      case "streak_20":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.streak_20.title),
          description: t(($) => $.honor_agent.achievements.streak_20.description),
        };
      case "memory_spark":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.memory_spark.title),
          description: t(($) => $.honor_agent.achievements.memory_spark.description),
        };
      case "memory_archive":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.memory_archive.title),
          description: t(($) => $.honor_agent.achievements.memory_archive.description),
        };
      case "memory_constellation":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.memory_constellation.title),
          description: t(($) => $.honor_agent.achievements.memory_constellation.description),
        };
      case "evolution_seed":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.evolution_seed.title),
          description: t(($) => $.honor_agent.achievements.evolution_seed.description),
        };
      case "evolution_engine":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.evolution_engine.title),
          description: t(($) => $.honor_agent.achievements.evolution_engine.description),
        };
      case "deep_space_explorer":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.deep_space_explorer.title),
          description: t(
            ($) => $.honor_agent.achievements.deep_space_explorer.description,
          ),
        };
      case "phoenix_protocol":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.phoenix_protocol.title),
          description: t(($) => $.honor_agent.achievements.phoenix_protocol.description),
        };
      case "corvette_command":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.corvette_command.title),
          description: t(($) => $.honor_agent.achievements.corvette_command.description),
        };
      case "cruiser_command":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.cruiser_command.title),
          description: t(($) => $.honor_agent.achievements.cruiser_command.description),
        };
      case "dreadnought_command":
        return {
          ...fallback,
          title: t(($) => $.honor_agent.achievements.dreadnought_command.title),
          description: t(
            ($) => $.honor_agent.achievements.dreadnought_command.description,
          ),
        };
      default:
        return fallback;
    }
  };
}
