// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhSettings from "../locales/zh-Hans/settings.json";
import { HonorWall } from "./honor-wall";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof zhSettings) => unknown) =>
      String(selector(zhSettings) ?? ""),
  }),
}));

describe("HonorWall", () => {
  it("localizes server badge titles on the public showcase and recent list", () => {
    render(
      <HonorWall
        wall={{
          level: 12,
          name_style: "default",
          badges_unlocked: 2,
          badges_total: 51,
          unlocked_badges: [],
          showcase_badges: [
            {
              id: "earth",
              title: "Earth",
              description: "Reach level 10.",
              svg_key: "earth",
            },
          ],
          recent_unlocks: [
            {
              id: "mars",
              title: "Mars",
              description: "Reach level 12.",
              svg_key: "mars",
              unlocked_at: "2026-08-04T00:00:00Z",
            },
          ],
        }}
        completionLabel="完成度"
        statsLabel="2 / 51"
        showcaseTitle="荣誉展柜"
        recentTitle="最近解锁"
      />,
    );

    expect(screen.getAllByText("蓝星锚点").length).toBeGreaterThan(0);
    expect(screen.getAllByText("火星拓荒者").length).toBeGreaterThan(0);
    expect(screen.queryByText("Earth")).not.toBeInTheDocument();
    expect(screen.queryByText("Mars")).not.toBeInTheDocument();
  });
});
