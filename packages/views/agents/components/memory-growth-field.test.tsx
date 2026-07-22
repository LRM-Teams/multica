// @vitest-environment jsdom

import { describe, it, expect, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { computeMemoryGrowth } from "@multica/core/agents";
import { MemoryGrowthField } from "./memory-growth-field";
import { MemoryGrowthSection } from "./memory-growth-section";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents },
};

afterEach(() => {
  cleanup();
});

describe("MemoryGrowthField (LRM-304 scheme A)", () => {
  it("renders dot, tier, four segments, and next progress", () => {
    const growth = computeMemoryGrowth(5);
    expect(growth).not.toBeNull();
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MemoryGrowthField
          growth={growth!}
          title="Memory growth"
          nextLabel={(tier) => `Next · ${tier}`}
          writesLabel={(c, r) => `${c} / ${r} writes`}
        />
      </I18nProvider>,
    );

    expect(screen.getByTestId("memory-growth-field")).toBeTruthy();
    expect(screen.getByText("Memory growth")).toBeTruthy();
    expect(screen.getByText("Silver")).toBeTruthy();
    expect(screen.getByText("Next · Gold")).toBeTruthy();
    expect(screen.getByText("5 / 6 writes")).toBeTruthy();

    const segments = screen
      .getByTestId("memory-growth-field")
      .querySelectorAll("[data-status]");
    expect(segments).toHaveLength(4);
    expect(segments[0]?.getAttribute("data-status")).toBe("complete");
    expect(segments[1]?.getAttribute("data-status")).toBe("current");
    expect(segments[2]?.getAttribute("data-status")).toBe("upcoming");
    expect(segments[3]?.getAttribute("data-status")).toBe("upcoming");
  });

  it("hides next row at max tier", () => {
    const growth = computeMemoryGrowth(24);
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MemoryGrowthField growth={growth!} />
      </I18nProvider>,
    );
    expect(screen.getByText("Platinum")).toBeTruthy();
    expect(screen.queryByText(/Next ·/)).toBeNull();
  });
});

describe("MemoryGrowthSection", () => {
  it("renders when server payload is present", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MemoryGrowthSection growth={computeMemoryGrowth(5)} />
      </I18nProvider>,
    );
    expect(screen.getByTestId("memory-growth-field")).toBeTruthy();
    expect(screen.getByText("Silver")).toBeTruthy();
    expect(screen.getByText("Next · Gold")).toBeTruthy();
  });
});
