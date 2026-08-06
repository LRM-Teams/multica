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
});
