// @vitest-environment jsdom

import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import zhSettings from "../locales/zh-Hans/settings.json";
import { HonorUnlockListener } from "./honor-unlock-listener";

const mocks = vi.hoisted(() => ({
  eventHandlers: new Map<string, (payload: unknown) => void>(),
  invalidateQueries: vi.fn(),
  toastCustom: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "workspace-1",
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    mocks.eventHandlers.set(event, handler);
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  workspaceKeys: {
    members: (workspaceId: string) => ["workspaces", workspaceId, "members"],
  },
}));

vi.mock("sonner", () => ({
  toast: {
    custom: mocks.toastCustom,
    dismiss: vi.fn(),
  },
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      selector: (bundle: typeof zhSettings) => unknown,
      options?: Record<string, string | number>,
    ) => {
      const template = selector(zhSettings);
      if (typeof template !== "string") return String(template ?? "");
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(options?.[key] ?? `{{${key}}}`),
      );
    },
    i18n: { language: "zh-Hans", resolvedLanguage: "zh-Hans" },
  }),
}));

describe("HonorUnlockListener", () => {
  beforeEach(() => {
    mocks.eventHandlers.clear();
    mocks.invalidateQueries.mockReset();
    mocks.toastCustom.mockReset();
  });

  it("localizes the badge carried by the realtime unlock event", () => {
    render(<HonorUnlockListener />);

    act(() => {
      mocks.eventHandlers.get("honor:badge_unlocked")?.({
        user_id: "user-1",
        badge: {
          id: "stardust",
          title: "Stardust",
          description: "Reach level 3.",
          svg_key: "stardust",
        },
        unlock_pct: 12,
      });
    });

    const toastRenderer = mocks.toastCustom.mock.calls[0]?.[0];
    expect(toastRenderer).toBeTypeOf("function");
    render(toastRenderer("toast-1"));

    expect(screen.getAllByText("星尘初光").length).toBeGreaterThan(0);
    expect(screen.queryByText("Stardust")).not.toBeInTheDocument();
  });
});
