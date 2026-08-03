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

  it("prefers a concrete API message when provided", () => {
    render(
      <ResearchServerErrorPage onRetry={() => {}} message="502 Bad Gateway" />,
    );
    expect(screen.getByText("502 Bad Gateway")).toBeTruthy();
  });

  it("disables retry while retrying", () => {
    render(<ResearchServerErrorPage onRetry={() => {}} retrying />);
    const btn = screen.getByTestId("research-server-error-retry");
    expect((btn as HTMLButtonElement).disabled).toBe(true);
  });
});
