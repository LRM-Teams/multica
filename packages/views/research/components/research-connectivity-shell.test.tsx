import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { ResearchConnectivityShell } from "./research-connectivity-shell";

const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({
    invalidateQueries: vi.fn(),
    fetchQuery: vi.fn().mockResolvedValue({ sessions: [] }),
  }),
}));

vi.mock("@multica/core/research", () => ({
  researchKeys: { all: (wsId: string) => ["research", wsId] },
  researchSessionListOptions: (wsId: string) => ({
    queryKey: ["research", wsId, "sessions"],
  }),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        connectivity: {
          offline_title: "You are offline",
          offline_hint: "Stay put.",
          reconnecting_title: "Reconnecting…",
          reconnecting_hint: "Refreshing…",
          reconnected: "Reconnected",
          reconnect_failed: "Reconnect failed toast",
          reconnect_failed_title: "Reconnect failed",
          reconnect_failed_hint: "Try again.",
          retry: "Retry",
          retrying: "Retrying…",
        },
      }),
  }),
}));

describe("ResearchConnectivityShell (LRM-833)", () => {
  beforeEach(() => {
    toastSuccess.mockReset();
    toastError.mockReset();
    Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
  });

  afterEach(() => {
    Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
  });

  it("keeps children mounted under an offline banner", () => {
    Object.defineProperty(navigator, "onLine", { configurable: true, value: false });
    render(
      <ResearchConnectivityShell>
        <div data-testid="research-body">session body</div>
      </ResearchConnectivityShell>,
    );
    expect(screen.getByTestId("research-offline-banner").getAttribute("data-mode")).toBe(
      "offline",
    );
    expect(screen.getByTestId("research-body").textContent).toContain("session body");
  });


});
