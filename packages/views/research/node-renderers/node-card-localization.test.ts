import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import en from "../../locales/en/research.json";
import zh from "../../locales/zh-Hans/research.json";
import { NODE_CARD_STATES } from "./node-state-matrix";

const here = dirname(fileURLToPath(import.meta.url));
const shell = readFileSync(join(here, "node-card-shell.tsx"), "utf8");
const matrix = readFileSync(join(here, "node-state-matrix.ts"), "utf8");

describe("Research V6 node-card localization", () => {
  it("has one locale label for every canonical state", () => {
    expect(Object.keys(en.node_card.states).sort()).toEqual(
      [...NODE_CARD_STATES].sort(),
    );
    expect(Object.keys(zh.node_card.states).sort()).toEqual(
      [...NODE_CARD_STATES].sort(),
    );
  });

  it("keeps the visual matrix locale-neutral", () => {
    expect(matrix).not.toMatch(/[\u4e00-\u9fff]/u);
    for (const state of NODE_CARD_STATES) {
      expect(matrix).toContain(`label: "${state}"`);
    }
  });

  it("routes generated card-face and state copy through node_card locale keys", () => {
    for (const key of [
      "owner",
      "objective",
      "current_action",
      "resolved",
      "progress",
      "risk",
    ]) {
      expect(shell).toContain(`$.node_card.${key}`);
    }
    expect(shell).toContain("$.node_card.states[state]");
  });
});
