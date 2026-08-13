import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchServerErrorPage } from "./research-server-error-page";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        connectivity: {
          server_error_title: "Server error",
          server_error_hint: "The research service returned an error. You can retry.",
          technical_details: "Technical details",
          retry: "Retry",
          retrying: "Retrying…",
        },
      }),
  }),
}));

describe("ResearchServerErrorPage (LRM-833)", () => {
  it("renders title, hint, and retry CTA", () => {
    const onRetry = vi.fn();
    render(<ResearchServerErrorPage onRetry={onRetry} />);
    expect(screen.getByTestId("research-server-error-page")).toBeTruthy();
    expect(screen.getByText("Server error")).toBeTruthy();
    // LRM-1106 Gate: error title uses font-medium (not font-semibold).
    expect(screen.getByText("Server error").className).toContain("font-medium");
    expect(screen.getByText("Server error").className).not.toContain("font-semibold");
    fireEvent.click(screen.getByTestId("research-server-error-retry"));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("keeps the API message in collapsed technical details", () => {
    render(
      <ResearchServerErrorPage onRetry={() => {}} message="502 Bad Gateway" />,
    );
    expect(
      screen.getByText("The research service returned an error. You can retry."),
    ).toBeTruthy();
    const diagnostics = screen.getByTestId("research-server-error-diagnostics");
    expect(diagnostics).not.toHaveAttribute("open");
    expect(diagnostics).toHaveTextContent("502 Bad Gateway");
  });

  it("keeps retry focused but suppresses repeat activation while retrying", () => {
    const onRetry = vi.fn();
    render(<ResearchServerErrorPage onRetry={onRetry} retrying />);
    const btn = screen.getByTestId("research-server-error-retry") as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(btn).toHaveAttribute("aria-disabled", "true");
    btn.focus();
    fireEvent.click(btn);
    expect(document.activeElement).toBe(btn);
    expect(onRetry).not.toHaveBeenCalled();
  });
});
