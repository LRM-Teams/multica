export type FleetClassId =
  | "reserve"
  | "corvette"
  | "frigate"
  | "cruiser"
  | "battleship"
  | "dreadnought";

export interface AgentFleetPillarScores {
  delivery: number;
  evolution: number;
  growth: number;
  efficiency: number;
}

export interface AgentFleetRank {
  agent_id: string;
  fleet_score: number;
  class_id: FleetClassId | string;
  class_label: string;
  fleet_rank: number;
  fleet_size: number;
  sample_tasks: number;
  sample_sufficient: boolean;
  frozen: boolean;
  pillars: AgentFleetPillarScores;
}

export interface AgentFleetRulesDocument {
  version: string;
  window_days: number;
  min_sample_tasks: number;
  pillar_weights: Record<string, number>;
  class_thresholds: Array<{
    class_id: string;
    min_score: number;
    label: string;
    svg_key: string;
  }>;
  changelog: string[];
}
