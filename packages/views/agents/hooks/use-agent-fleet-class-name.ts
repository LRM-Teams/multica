"use client";

import { useT } from "../../i18n";

export function useAgentFleetClassName() {
  const { t } = useT("agents");

  return (classId: string, fallback?: string): string => {
    switch (classId) {
      case "reserve":
        return t(($) => $.honor_agent.fleet_classes.reserve);
      case "corvette":
        return t(($) => $.honor_agent.fleet_classes.corvette);
      case "frigate":
        return t(($) => $.honor_agent.fleet_classes.frigate);
      case "cruiser":
        return t(($) => $.honor_agent.fleet_classes.cruiser);
      case "battleship":
        return t(($) => $.honor_agent.fleet_classes.battleship);
      case "dreadnought":
        return t(($) => $.honor_agent.fleet_classes.dreadnought);
      default:
        return fallback?.trim() || classId;
    }
  };
}
