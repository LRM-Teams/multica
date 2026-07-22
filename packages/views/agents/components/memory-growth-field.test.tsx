// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, render, screen, act } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentMemoryGrowth } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import {
  MEMORY_GROWTH_PULSE_MS,
  MemoryGrowthField,
} from "./memory-growth-field";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents },
};

function silverGrowth(overrides: Partial<AgentMemoryGrowth> = {}): AgentMemoryGrowth {
  return {
    total_writes: 5,
    tier: "silver",
    tier_label: "Silver",
    segments: [
      { tier: "bronze", tier_label: "Bronze", status: "complete" },
      { tier: "silver", tier_label: "Silver", status: "current" },
      { tier: "gold", tier_label: "Gold", status: "upcoming" },
      { tier: "platinum", tier_label: "Platinum", status: "upcoming" },
    ],
    next: { tier: "gold", tier_label: "Gold", current: 5, required: 6 },
    ...overrides,
  };
}

function renderGrowth(growth: AgentMemoryGrowth | null | undefined) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <MemoryGrowthField growth={growth} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("MemoryGrowthField (LRM-304)", () => {
  it("renders nothing for missing / zero growth (no placeholder flash)", () => {
    const { container: a } = renderGrowth(undefined);
    expect(a.querySelector("[data-testid='memory-growth-field']")).toBeNull();
    cleanup();
    const { container: b } = renderGrowth(null);
    expect(b.querySelector("[data-testid='memory-growth-field']")).toBeNull();
    cleanup();
    const { container: c } = renderGrowth(silverGrowth({ total_writes: 0 }));
    expect(c.querySelector("[data-testid='memory-growth-field']")).toBeNull();
  });

  it("renders Slack-style field: label, tier, segments, next · n/m writes", () => {
    renderGrowth(silverGrowth());
    expect(screen.getByTestId("memory-growth-field")).toBeTruthy();
    expect(screen.getByText("Memory growth")).toBeTruthy();
    expect(screen.getByTestId("memory-growth-tier")).toHaveTextContent("Silver");
    const segments = screen.getByTestId("memory-growth-segments");
    expect(segments.querySelectorAll("[data-status]")).toHaveLength(4);
    expect(segments.querySelector('[data-status="complete"]')).toBeTruthy();
    expect(segments.querySelector('[data-status="current"]')).toBeTruthy();
    expect(screen.getByTestId("memory-growth-next-tier")).toHaveTextContent(
      "Next · Gold",
    );
    expect(screen.getByTestId("memory-growth-writes")).toHaveTextContent(
      "5 / 6 writes",
    );
    expect(screen.getByTestId("memory-growth-fine-bar")).toBeTruthy();
  });

  it("pulses ≤400ms when tier advances", () => {
    const { rerender } = render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MemoryGrowthField
          growth={silverGrowth({
            total_writes: 2,
            tier: "bronze",
            tier_label: "Bronze",
            next: { tier: "silver", tier_label: "Silver", current: 2, required: 3 },
            segments: [
              { tier: "bronze", tier_label: "Bronze", status: "current" },
              { tier: "silver", tier_label: "Silver", status: "upcoming" },
              { tier: "gold", tier_label: "Gold", status: "upcoming" },
              { tier: "platinum", tier_label: "Platinum", status: "upcoming" },
            ],
          })}
        />
      </I18nProvider>,
    );
    expect(screen.getByTestId("memory-growth-tier").className).not.toContain(
      "memory-growth-pulse",
    );

    rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MemoryGrowthField growth={silverGrowth()} />
      </I18nProvider>,
    );
    expect(screen.getByTestId("memory-growth-tier").className).toContain(
      "memory-growth-pulse",
    );

    act(() => {
      vi.advanceTimersByTime(MEMORY_GROWTH_PULSE_MS);
    });
    expect(screen.getByTestId("memory-growth-tier").className).not.toContain(
      "memory-growth-pulse",
    );
  });

  it("hides fine progress when next is omitted (maxed platinum)", () => {
    renderGrowth(
      silverGrowth({
        total_writes: 30,
        tier: "platinum",
        tier_label: "Platinum",
        next: null,
        segments: [
          { tier: "bronze", tier_label: "Bronze", status: "complete" },
          { tier: "silver", tier_label: "Silver", status: "complete" },
          { tier: "gold", tier_label: "Gold", status: "complete" },
          { tier: "platinum", tier_label: "Platinum", status: "current" },
        ],
      }),
    );
    expect(screen.getByTestId("memory-growth-tier")).toHaveTextContent("Platinum");
    expect(screen.queryByTestId("memory-growth-next-tier")).toBeNull();
    expect(screen.queryByTestId("memory-growth-fine-bar")).toBeNull();
  });
});
