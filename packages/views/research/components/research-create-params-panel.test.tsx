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

describe("ResearchCreateParamsPanel (LRM-838)", () => {
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

  it("done closes the panel", () => {
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
});
