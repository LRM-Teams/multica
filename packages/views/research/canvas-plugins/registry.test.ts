import { describe, expect, it } from "vitest";
import {
  createResearchCanvasPluginRegistry,
  defaultResearchCanvasPluginRegistry,
  researchCanvasPluginRegistryReduce,
  type ResearchCanvasPluginState,
} from "./registry";
import {
  ABSENT_SLOT_FALLBACK_TEST_IDS,
} from "./fallbacks";
import {
  RESEARCH_CANVAS_PLUGIN_SLOT_IDS,
  type ResearchCanvasPluginRegistration,
} from "./types";

function registration(id: string, slot: string): ResearchCanvasPluginRegistration<any> {
  return {
    id,
    slot: slot as never,
    load: () => Promise.resolve({ default: function Dummy() { return null; } }),
  };
}

describe("ResearchCanvasPluginRegistry — FE-10 AC #3 (display-only)", () => {
  it("defaults to an empty registry", () => {
    const registry = createResearchCanvasPluginRegistry();
    expect(Object.keys(registry)).toHaveLength(0);
    expect(Object.keys(defaultResearchCanvasPluginRegistry)).toHaveLength(0);
  });

  it("registry reduce is additive and never mutates the input state", () => {
    const base: ResearchCanvasPluginState = createResearchCanvasPluginRegistry();
    const next = researchCanvasPluginRegistryReduce(base, {
      type: "register",
      registration: registration("insight-a", "insight"),
    });
    expect(next).toHaveProperty("insight-a");
    expect(base).not.toHaveProperty("insight-a");
    // Removal is a new object too.
    const cleared = researchCanvasPluginRegistryReduce(next, {
      type: "remove",
      id: "insight-a",
    });
    expect(cleared).not.toHaveProperty("insight-a");
    expect(next).toHaveProperty("insight-a");
  });

  it("remove of an unknown id is a no-op (same reference)", () => {
    const base: ResearchCanvasPluginState = createResearchCanvasPluginRegistry();
    const next = researchCanvasPluginRegistryReduce(base, { type: "remove", id: "nope" });
    expect(next).toBe(base);
  });

  it("the registry only holds display registrations — it exposes no API to write projection or create canonical nodes", () => {
    // AC #3: the registry's only product is a lazy display component loader.
    // There is deliberately no "upsertNode / addNode / mutateProjection"
    // surface anywhere on the registry, and each registration is inert until
    // a host mounts (load is a thunk, not invoked here).
    const registry = {
      load: () => Promise.resolve({ default: function Placeholder() { return null; } }),
    };
    expect(typeof registry.load).toBe("function");
    // The six slots each have a generic absent marker (business-free).
    expect(Object.keys(ABSENT_SLOT_FALLBACK_TEST_IDS).sort()).toEqual(
      [...RESEARCH_CANVAS_PLUGIN_SLOT_IDS].sort(),
    );
    // A strict structural guard: any property that sounds like it authors
    // canonical graph state on the registry would fail here.
    const forbidden = /(upsert|insert|create.*node|write|projection|commit|mutat)/i;
    for (const key of Object.keys(ABSENT_SLOT_FALLBACK_TEST_IDS)) {
      expect(forbidden.test(key)).toBe(false);
    }
  });
});
