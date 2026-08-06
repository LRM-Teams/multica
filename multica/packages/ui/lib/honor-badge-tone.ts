type HonorBadgeTone = "gold" | "cyan" | "violet" | "amber" | "emerald" | "neutral";

const expandedCatalogTones: Record<string, HonorBadgeTone> = {
  moon: "cyan",
  comet: "amber",
  asteroid: "neutral",
  eclipse: "violet",
  pulsar: "cyan",
  solar_sail: "gold",
  orbital_station: "cyan",
  lunar_base: "emerald",
  pathfinder: "emerald",
  voyager: "cyan",
  beacon: "gold",
  relay: "violet",
  archive: "cyan",
  constellation: "violet",
  aurora: "emerald",
  galaxy: "violet",
  wormhole: "cyan",
  terraformer: "emerald",
  foundry: "amber",
  nexus: "cyan",
  helix: "emerald",
  prism_core: "violet",
  plasma_orb: "violet",
  quantum_gate: "cyan",
  singularity: "violet",
  celestial_crown: "gold",
  event_horizon: "amber",
  cosmic_tree: "emerald",
  infinity: "gold",
  photon_ring: "cyan",
  chronosphere: "gold",
  diamond_star: "emerald",
  supernova: "gold",
  black_hole: "violet",
};

export function honorBadgeTone(svgKey: string): HonorBadgeTone {
  const catalogTone = expandedCatalogTones[svgKey];
  if (catalogTone) return catalogTone;
  if (svgKey.includes("genesis") || svgKey === "founding") return "gold";
  if (svgKey.includes("quasar") || svgKey.includes("blue") || svgKey.includes("neptune")) return "cyan";
  if (svgKey.includes("red") || svgKey.includes("mars")) return "amber";
  if (svgKey.includes("jupiter") || svgKey.includes("saturn") || svgKey.includes("venus")) return "gold";
  if (svgKey.includes("earth") || svgKey.includes("twin") || svgKey.includes("forge")) return "emerald";
  if (svgKey.includes("pluto") || svgKey.includes("mercury") || svgKey.includes("stardust")) return "neutral";
  return "violet";
}
