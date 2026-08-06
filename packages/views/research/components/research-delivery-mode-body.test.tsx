import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  ResearchDeliveryModeBody,
  ResearchDeliveryModeChip,
} from "./research-delivery-mode-body";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        panel: {
          delivery_mode: {
            empty: "Idle",
            loading: "Loading",
            running: "In progress",
            error: "Error",
          },
        },
        reader: {
          empty_title: "No delivery yet",
          empty_body: "Empty body",
          loading_body: "Assembling…",
          error_title: "Could not load delivery",
          error_body: "Error body",
        },
        session_page: { retry: "Retry" },
      };
      return picker(keys as never);
    },
  }),
}));

describe("ResearchDeliveryModeBody / Chip", () => {
  it("renders designed empty / loading / error bodies", () => {
    const { rerender } = render(<ResearchDeliveryModeBody mode="empty" />);
    expect(screen.getByTestId("research-delivery-empty")).toBeInTheDocument();
    expect(screen.getByText("No delivery yet")).toBeInTheDocument();

    rerender(<ResearchDeliveryModeBody mode="loading" />);
    expect(screen.getByTestId("research-delivery-loading")).toBeInTheDocument();

    rerender(<ResearchDeliveryModeBody mode="error" onRetry={() => {}} />);
    expect(screen.getByTestId("research-delivery-error")).toBeInTheDocument();
    expect(screen.getByText("Retry")).toBeInTheDocument();
  });

  it("chip exposes data-delivery-mode", () => {
    render(<ResearchDeliveryModeChip mode="running" />);
    expect(screen.getByTestId("research-delivery-mode")).toHaveAttribute(
      "data-delivery-mode",
      "running",
    );
  });

  // Delivery flips loading → running inside one mount, so the announcement
  // must live on a node that survives every mode change (same fix shape as
  // LRM-1225 chat chip + M2 aux cards). A live region that only exists on the
  // loading branch is unmounted exactly when the content becomes ready.
  it("keeps one persistent live region across every mode flip", () => {
    const { rerender } = render(<ResearchDeliveryModeChip mode="loading" />);
    const live = screen.getByTestId("research-delivery-mode-live");
    expect(live).toHaveAttribute("aria-live", "polite");
    expect(live).toHaveAttribute("aria-busy", "true");
    expect(live).toHaveTextContent("Loading");

    rerender(<ResearchDeliveryModeChip mode="running" />);
    // Same DOM node — a remount would reset the live region and swallow the
    // ready announcement.
    expect(screen.getByTestId("research-delivery-mode-live")).toBe(live);
    expect(live).toHaveAttribute("aria-busy", "false");
    expect(live).toHaveTextContent("In progress");

    rerender(<ResearchDeliveryModeChip mode="error" />);
    expect(screen.getByTestId("research-delivery-mode-live")).toBe(live);
    expect(live).toHaveTextContent("Error");
  });

  it("does not duplicate the mode announcement on the loading body", () => {
    render(<ResearchDeliveryModeBody mode="loading" />);
    const loading = screen.getByTestId("research-delivery-loading");
    // aria-busy stays (it describes the region), but the announcement is owned
    // by the persistent chip region — two live regions would double-speak.
    expect(loading).toHaveAttribute("aria-busy");
    expect(loading).not.toHaveAttribute("aria-live");
  });
});
