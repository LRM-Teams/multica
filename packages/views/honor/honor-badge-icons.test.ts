// @vitest-environment node
import { describe, expect, it } from "vitest";
import { HONOR_BADGE_ICONS } from "@multica/ui/components/honor/honor-badge-icons";

const expandedCatalogIconKeys = [
  "moon",
  "comet",
  "asteroid",
  "eclipse",
  "pulsar",
  "solar_sail",
  "orbital_station",
  "lunar_base",
  "pathfinder",
  "voyager",
  "beacon",
  "relay",
  "archive",
  "constellation",
  "aurora",
  "galaxy",
  "wormhole",
  "terraformer",
  "foundry",
  "nexus",
  "helix",
  "prism_core",
  "plasma_orb",
  "quantum_gate",
  "singularity",
  "celestial_crown",
  "event_horizon",
  "cosmic_tree",
  "infinity",
  "photon_ring",
  "chronosphere",
  "diamond_star",
  "supernova",
  "black_hole",
] as const;

describe("expanded honor badge icon catalog", () => {
  it("provides a purpose-built cosmic icon for every added badge", () => {
    expect(expandedCatalogIconKeys).toHaveLength(34);
    for (const svgKey of expandedCatalogIconKeys) {
      expect(HONOR_BADGE_ICONS[svgKey], svgKey).toBeTypeOf("function");
    }
  });
});
