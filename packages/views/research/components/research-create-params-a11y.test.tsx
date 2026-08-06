// @vitest-environment jsdom

/**
 * LRM-1237 — create-params sheet: depth/language radiogroup accessible names
 * + depth error describedby. No list-page / LRM-1236 CTA pending scope.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import enResearch from "../../locales/en/research.json";
import { defaultCreateParams } from "../lib/research-create-params";
import { ResearchCreateParamsPanel } from "./research-create-params-panel";

const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = "research-create-params-panel.tsx";

function readSrc() {
  return fs.readFileSync(path.join(here, SRC), "utf8");
}

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

describe("research-create-params a11y (LRM-1237)", () => {
  it("source: depth/language radiogroups wire labelledby; depth error describedby", () => {
    const src = readSrc();
    expect(src).toMatch(/aria-labelledby=\{depthTitleId\}/);
    expect(src).toMatch(/aria-labelledby=\{languageTitleId\}/);
    expect(src).toMatch(
      /aria-describedby=\{depthInvalid\s*\?\s*depthErrorId\s*:\s*undefined\}/,
    );
    expect(src).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
  });

  it("render: depth and language radiogroups expose accessible names", () => {
    render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={() => {}}
        onChange={() => {}}
      />,
    );
    const panel = screen.getByTestId("research-create-params-panel");
    expect(
      within(panel).getByRole("radiogroup", {
        name: enResearch.create_params.depth_label,
      }),
    ).toBeInTheDocument();
    expect(
      within(panel).getByRole("radiogroup", {
        name: enResearch.create_params.language_label,
      }),
    ).toBeInTheDocument();
  });

  it("render: depth invalid links radiogroup to role=alert via aria-describedby", () => {
    render(
      <ResearchCreateParamsPanel
        open
        value={{ ...defaultCreateParams("en"), depth_tier: "nope" as never }}
        errors={{ depth: "depth_invalid" }}
        onOpenChange={() => {}}
        onChange={() => {}}
      />,
    );
    const group = screen.getByRole("radiogroup", {
      name: enResearch.create_params.depth_label,
    });
    expect(group).toHaveAttribute("aria-invalid", "true");
    const error = screen.getByTestId("research-create-depth-error");
    expect(error).toHaveAttribute("role", "alert");
    expect(group.getAttribute("aria-describedby")).toBe(error.id);
    expect(error.id).toBeTruthy();
  });

  it("render: estimate keeps aria-live=polite (zero regression)", () => {
    render(
      <ResearchCreateParamsPanel
        open
        value={defaultCreateParams("en")}
        onOpenChange={() => {}}
        onChange={() => {}}
      />,
    );
    expect(screen.getByTestId("research-create-estimate")).toHaveAttribute(
      "aria-live",
      "polite",
    );
  });
});
