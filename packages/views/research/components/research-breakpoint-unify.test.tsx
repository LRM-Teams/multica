// @vitest-environment jsdom

/**
 * LRM-1109 — research breakpoint unify: useIsMobile (768) + Tailwind md:
 * must agree; 640–767 is no longer a dead zone where JS mobile coexists with
 * sm: desktop CSS.
 */
import { render, screen } from "@testing-library/react";
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import enResearch from "../../locales/en/research.json";
import { ResearchAuxDrawer } from "./research-aux-drawer";
import { ResearchChatDrawer } from "./research-chat-drawer";
import { SourceStrategyStrip } from "./source-strategy-strip";

const originalWidth = window.innerWidth;
const originalMatchMedia = window.matchMedia;

function setViewport(px: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: px,
    writable: true,
  });
  window.matchMedia = ((query: string) => {
    const max = /max-width:\s*(\d+)px/.exec(query);
    const min = /min-width:\s*(\d+)px/.exec(query);
    let matches = false;
    if (max) matches = px <= Number(max[1]);
    else if (min) matches = px >= Number(min[1]);
    return {
      matches,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    } as MediaQueryList;
  }) as typeof window.matchMedia;
}

afterEach(() => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: originalWidth,
    writable: true,
  });
  window.matchMedia = originalMatchMedia;
});

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = fn(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
    i18n: { language: "en" },
  }),
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: React.ReactNode;
  }) => (open ? <div data-testid="sheet-root">{children}</div> : null),
  SheetContent: ({
    children,
    className,
    ...rest
  }: {
    children?: React.ReactNode;
    className?: string;
    "data-testid"?: string;
    "data-placement"?: string;
    "data-panel"?: string;
  }) => (
    <div
      data-testid={rest["data-testid"]}
      data-placement={rest["data-placement"]}
      data-panel={rest["data-panel"]}
      className={className}
    >
      {children}
    </div>
  ),
  SheetHeader: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children?: React.ReactNode }) => <h2>{children}</h2>,
  SheetDescription: ({ children }: { children?: React.ReactNode }) => (
    <p>{children}</p>
  ),
}));

describe("LRM-1109 research breakpoint unify (matchMedia 360/700/768)", () => {
  describe("useIsMobile matchMedia tiers", () => {
    it.each([
      [360, true],
      [700, true],
      [767, true],
      [768, false],
    ] as const)("width %i → isMobile=%s", (width, expected) => {
      setViewport(width);
      const { result } = renderHook(() => useIsMobile());
      expect(result.current).toBe(expected);
    });
  });

  describe("JS layout branch follows the same tiers", () => {
    beforeEach(() => {
      setViewport(1024);
    });

    it.each([
      [360, "sheet"],
      [700, "sheet"],
      [767, "sheet"],
      [768, "float"],
    ] as const)("chat drawer at %ipx → %s", (width, placement) => {
      setViewport(width);
      render(
        <ResearchChatDrawer open onClose={() => {}}>
          <span>body</span>
        </ResearchChatDrawer>,
      );
      expect(screen.getByTestId("research-chat-drawer")).toHaveAttribute(
        "data-placement",
        placement,
      );
    });

    it("aux drawer stays full-bleed in the 640–767 dead zone (no sm:max-w-*)", () => {
      setViewport(700);
      render(
        <ResearchAuxDrawer panel="sources" onClose={() => {}}>
          <span>body</span>
        </ResearchAuxDrawer>,
      );
      const el = screen.getByTestId("research-aux-drawer");
      // LRM-1118 SoT: isMobile branch must not carry sm:* layout flips.
      expect(el.className).not.toMatch(/\bsm:max-w-/);
      expect(el.className).toMatch(/!max-w-none/);
    });
  });

  describe("CSS companions use md: (768), not sm: (640)", () => {

    it("source strategy strip grids at md (no sm:grid beside logic-strip)", () => {
      setViewport(700);
      render(
        <SourceStrategyStrip
          model={{
            chips: [
              {
                id: "a",
                label: "A",
                layer: "general",
                samples: [],
                why: "why",
              },
            ],
            whyLine: "why",
            empty: false,
          }}
          sessionStatus="running"
        />,
      );
      const cards = screen.getByTestId("source-strategy-cards");
      expect(cards.className).toMatch(/md:grid-cols-2/);
      expect(cards.className).not.toMatch(/\bsm:grid-cols/);
    });
  });
});
