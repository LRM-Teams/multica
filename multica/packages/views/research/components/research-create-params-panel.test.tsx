import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import enResearch from "../../locales/en/research.json";
import { defaultCreateParams } from "../lib/research-create-params";
import { ResearchCreateParamsPanel } from "./research-create-params-panel";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown) => fn(enResearch),
    i18n: { language: "en" },
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: ReactNode;
  }) => (open ? <div data-testid="sheet-root">{children}</div> : null),
  SheetContent: ({
    children,
    ...rest
  }: {
    children?: ReactNode;
    "data-testid"?: string;
    className?: string;
  }) => (
    <div data-testid={rest["data-testid"] ?? "sheet-content"} className={rest.className}>
      {children}
    </div>
  ),
  SheetHeader: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children?: ReactNode }) => <h2>{children}</h2>,
  SheetDescription: ({ children }: { children?: ReactNode }) => <p>{children}</p>,
}));

describe("ResearchCreateParamsPanel (LRM-838 / LRM-835 / LRM-839)", () => {
  it("renders adjustable depth, weights, and language controls", () => {
    const onChange = vi.fn();
    render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={() => {}}
        onChange={onChange}
      />,
    );
    expect(screen.getByTestId("research-create-params-panel")).toBeInTheDocument();
    expect(screen.getByTestId("research-create-depth")).toBeInTheDocument();
    expect(screen.getByTestId("research-create-weights")).toBeInTheDocument();
    expect(screen.getByTestId("research-create-language")).toBeInTheDocument();
    expect(
      screen.getByText(enResearch.create_params.depth_tiers.standard.hint),
    ).toBeInTheDocument();
    expect(
      screen.getByText(enResearch.create_params.weight_rows.primary.hint),
    ).toBeInTheDocument();
  });

  it("shows linked estimate and refreshes when depth changes (LRM-839)", () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={() => {}}
        onChange={onChange}
      />,
    );
    const estimate = screen.getByTestId("research-create-estimate");
    expect(estimate).toHaveAttribute("data-estimate-status", "ready");
    expect(estimate).toHaveTextContent(enResearch.create_estimate.badge);
    expect(screen.getByTestId("research-create-estimate-cost")).toHaveTextContent(
      enResearch.create_estimate.cost_tiers.medium,
    );

    const deep = { ...defaultCreateParams("en"), depth_tier: "deep" as const };
    rerender(
      <ResearchCreateParamsPanel
        open
        value={deep}
        onOpenChange={() => {}}
        onChange={onChange}
      />,
    );
    expect(screen.getByTestId("research-create-estimate")).toHaveAttribute(
      "data-estimate-status",
      "ready",
    );
    expect(screen.getByTestId("research-create-estimate-cost")).toHaveTextContent(
      enResearch.create_estimate.cost_tiers.high,
    );

    const shallow = {
      ...defaultCreateParams("en"),
      depth_tier: "shallow" as const,
    };
    rerender(
      <ResearchCreateParamsPanel
        open
        value={shallow}
        onOpenChange={() => {}}
        onChange={onChange}
      />,
    );
    expect(screen.getByTestId("research-create-estimate-cost")).toHaveTextContent(
      enResearch.create_estimate.cost_tiers.low,
    );
  });

  it("shows unknown estimate without blocking Done (LRM-839)", () => {
    const onOpenChange = vi.fn();
    render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={onOpenChange}
        onChange={() => {}}
        estimateResolveOptions={{ lookup: () => null }}
      />,
    );
    expect(screen.getByTestId("research-create-estimate")).toHaveAttribute(
      "data-estimate-status",
      "unknown",
    );
    expect(screen.getByTestId("research-create-estimate-duration")).toHaveTextContent(
      enResearch.create_estimate.unknown,
    );
    fireEvent.click(screen.getByTestId("research-create-params-done"));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("selecting deep depth calls onChange", () => {
    const onChange = vi.fn();
    render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={() => {}}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByText(enResearch.create_params.depth_tiers.deep.label));
    expect(onChange).toHaveBeenCalled();
    const next = onChange.mock.calls.at(-1)?.[0];
    expect(next?.depth_tier).toBe("deep");
  });

  it("done closes the panel when params are valid", () => {
    const onOpenChange = vi.fn();
    render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={onOpenChange}
        onChange={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("research-create-params-done"));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("shows near-field weight error and keeps out-of-range value (LRM-835)", () => {
    const onChange = vi.fn();
    const onErrorsChange = vi.fn();
    const onOpenChange = vi.fn();
    const base = defaultCreateParams("en");
    render(
      <ResearchCreateParamsPanel
        open
        value={{
          ...base,
          source_weights: { ...base.source_weights, primary: 1.4 },
        }}
        onOpenChange={onOpenChange}
        onChange={onChange}
        onErrorsChange={onErrorsChange}
      />,
    );
    expect(screen.getByTestId("research-create-weight-primary-error")).toHaveTextContent(
      enResearch.create_params.errors.weight_out_of_range,
    );
    const input = screen.getByTestId(
      "research-create-weight-primary-input",
    ) as HTMLInputElement;
    expect(input.value).toBe("1.4");
    fireEvent.click(screen.getByTestId("research-create-params-done"));
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(onErrorsChange).toHaveBeenCalled();
    const errs = onErrorsChange.mock.calls.at(-1)?.[0];
    expect(errs?.weights?.primary).toBe("weight_out_of_range");
  });
});
