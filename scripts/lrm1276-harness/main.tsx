/**
 * LRM-1276 evidence harness — proves the token move is behaviour-neutral.
 *
 * Why a harness and not only Vitest: the Vitest scan proves the class contract
 * in source. What it cannot prove is that the six containers still *scroll the
 * same way* after the declaration moved out of Tailwind's arbitrary-property
 * path and into a plain rule in `base.css`. That needs a real CSS build plus
 * real layout, so this renders both gates through the actual Tailwind pipeline.
 *
 * Note on `-webkit-overflow-scrolling` itself: it is an iOS-Safari-only
 * property, so no desktop engine reports it via `getComputedStyle`. The
 * declaration's survival is therefore checked against the emitted stylesheet
 * (`document.styleSheets`), not computed style — see `assertions` below. Real
 * iOS behaviour is out of scope here and belongs to LRM-1222.
 *
 * BEFORE = the six `origin/dev` class strings verbatim.
 * AFTER  = the same strings with the arbitrary property swapped for the class.
 */
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./harness.css";

const RAW = "[-webkit-overflow-scrolling:touch]";
const TOKEN = "momentum-scroll";

/** Container class strings, AFTER form. BEFORE is derived by swapping back. */
const CONTAINERS = [
  {
    id: "add-people",
    label: "channel-add-people-dialog.tsx:169 (y)",
    axis: "y" as const,
    className:
      "min-h-0 flex-1 overflow-y-auto overscroll-contain momentum-scroll pb-2",
  },
  {
    id: "members-roster",
    label: "channel-members-list.tsx:505 (y)",
    axis: "y" as const,
    className:
      "min-h-0 space-y-2 overflow-y-auto overscroll-contain px-5 py-3 momentum-scroll",
  },
  {
    id: "members-pending",
    label: "channel-members-list.tsx:539 (y)",
    axis: "y" as const,
    className:
      "min-h-0 overflow-y-auto overscroll-contain px-2 pb-2 momentum-scroll",
  },
  {
    id: "composer-tray",
    label: "composer-attachment-tray.tsx:132 (x)",
    axis: "x" as const,
    className:
      "m-0 flex list-none flex-row flex-nowrap items-center gap-3 overflow-x-auto overflow-y-hidden overscroll-x-contain p-0 pb-0.5 -mt-2 pr-2 pt-2 touch-pan-x momentum-scroll [scrollbar-width:thin]",
  },
  {
    id: "issues-header",
    label: "issues-header.tsx:571 (x)",
    axis: "x" as const,
    className: "h-12 shrink-0 overflow-x-auto px-4 momentum-scroll",
  },
  {
    id: "my-issues-header",
    label: "my-issues-header.tsx:40 (x)",
    axis: "x" as const,
    className: "h-12 shrink-0 overflow-x-auto px-4 momentum-scroll",
  },
];

const gate =
  new URLSearchParams(window.location.search).get("gate") === "before"
    ? "before"
    : "after";

const isBefore = gate === "before";

function classFor(className: string): string {
  return isBefore ? className.replace(TOKEN, RAW) : className;
}

type Probe = {
  id: string;
  axis: "x" | "y";
  scrollable: boolean;
  clientW: number;
  clientH: number;
  scrollW: number;
  scrollH: number;
  hasToken: boolean;
};

/**
 * Does the *built* stylesheet still carry the declaration?
 *
 * This deliberately reads the network response text rather than
 * `sheet.cssRules`: Chromium does not implement
 * `-webkit-overflow-scrolling`, so it drops the declaration at parse time and
 * the CSSOM would report an empty rule even when the CSS is correct. The
 * shipped bytes are what iOS Safari receives, so the bytes are the evidence.
 */
async function declarationInBuiltCss(): Promise<string[]> {
  const links = Array.from(
    document.querySelectorAll<HTMLLinkElement>('link[rel="stylesheet"]'),
  );
  const hits: string[] = [];

  for (const link of links) {
    const response = await fetch(link.href);
    const text = await response.text();

    for (const match of text.matchAll(
      /[^{}]*\{[^{}]*overflow-scrolling[^{}]*\}/g,
    )) {
      hits.push(match[0]);
    }
  }

  return hits;
}

function Row({
  label,
  axis,
  className,
  id,
}: {
  label: string;
  axis: "x" | "y";
  className: string;
  id: string;
}) {
  const items = Array.from({ length: 14 }, (_, index) => index + 1);
  // composer-attachment-tray is itself the flex row, so its chips are direct
  // children. The two headers wrap their tabs in a flex child — mirror that,
  // otherwise block-level children would never overflow on the x axis.
  const needsInnerFlex = axis === "x" && !className.includes("flex");

  const children = items.map((item) =>
    axis === "y" ? (
      <div key={item} className="rounded-md bg-muted/40 px-3 py-2 text-sm">
        成员条目 {item}
      </div>
    ) : (
      <div
        key={item}
        className="shrink-0 whitespace-nowrap rounded-md bg-muted/40 px-4 py-2 text-sm"
      >
        标签 {item}
      </div>
    ),
  );

  return (
    <section className="mb-5">
      <p className="mb-1 font-mono text-[11px] text-muted-foreground">{label}</p>
      <div
        className={
          axis === "y"
            ? "flex h-28 flex-col rounded-lg border border-border bg-card"
            : "rounded-lg border border-border bg-card"
        }
      >
        <div data-probe={id} className={className}>
          {needsInnerFlex ? (
            <div className="flex h-full flex-row items-center gap-3">
              {children}
            </div>
          ) : (
            children
          )}
        </div>
      </div>
    </section>
  );
}

function App() {
  const [probes, setProbes] = useState<Probe[]>([]);
  const [cssHits, setCssHits] = useState<string[] | null>(null);

  useEffect(() => {
    const next: Probe[] = CONTAINERS.map((container) => {
      const element = document.querySelector<HTMLElement>(
        `[data-probe="${container.id}"]`,
      );

      if (!element) {
        throw new Error(`missing probe: ${container.id}`);
      }

      const scrollable =
        container.axis === "y"
          ? element.scrollHeight > element.clientHeight
          : element.scrollWidth > element.clientWidth;

      return {
        id: container.id,
        axis: container.axis,
        scrollable,
        clientW: element.clientWidth,
        clientH: element.clientHeight,
        scrollW: element.scrollWidth,
        scrollH: element.scrollHeight,
        hasToken: element.classList.contains(TOKEN),
      };
    });

    setProbes(next);

    void declarationInBuiltCss().then((hits) => {
      setCssHits(hits);
      (window as unknown as { __LRM1276__: unknown }).__LRM1276__ = {
        gate,
        probes: next,
        cssHits: hits,
      };
    });
  }, []);

  const expectedSelector = isBefore
    ? ".\\[-webkit-overflow-scrolling\\:touch\\]"
    : ".momentum-scroll";
  const selectorPresent = (cssHits ?? []).some((hit) =>
    hit.startsWith(expectedSelector),
  );

  return (
    <main className="mx-auto max-w-5xl p-6">
      <h1 className="mb-1 text-lg font-bold">
        LRM-1276 · momentum-scroll token · gate={gate.toUpperCase()}
      </h1>
      <p className="mb-5 text-xs text-muted-foreground">
        {isBefore
          ? "BEFORE = origin/dev, declaration hand-copied inline per container."
          : "AFTER = single .momentum-scroll rule in packages/ui/styles/base.css."}
      </p>

      <div className="mb-6 rounded-lg border border-border bg-muted/20 p-3 font-mono text-[11px] leading-relaxed">
        <p className="mb-1 font-bold">
          built stylesheet · rules carrying -webkit-overflow-scrolling
        </p>
        {cssHits === null ? (
          <p>reading…</p>
        ) : cssHits.length === 0 ? (
          <p className="font-bold text-danger">
            NONE — declaration dropped by the CSS build
          </p>
        ) : (
          cssHits.map((hit) => <p key={hit}>{hit}</p>)
        )}
        {cssHits !== null && (
          <p
            className={
              selectorPresent
                ? "mt-1 font-bold text-success"
                : "mt-1 font-bold text-danger"
            }
          >
            {selectorPresent ? "PASS" : "FAIL"} · this gate expects{" "}
            {expectedSelector}
          </p>
        )}
      </div>

      <div className="mb-6 rounded-lg border border-border bg-muted/20 p-3 font-mono text-[11px] leading-relaxed">
        <p className="mb-1 font-bold">
          container geometry (must match across gates)
        </p>
        {probes.map((probe) => (
          <p key={probe.id}>
            {probe.id} · {probe.axis} · client {probe.clientW}×{probe.clientH} ·
            scroll {probe.scrollW}×{probe.scrollH} ·{" "}
            {probe.scrollable ? "SCROLLABLE" : "NOT-SCROLLABLE"} · token=
            {String(probe.hasToken)}
          </p>
        ))}
      </div>

      {CONTAINERS.map((container) => (
        <Row
          key={container.id}
          id={container.id}
          label={container.label}
          axis={container.axis}
          className={classFor(container.className)}
        />
      ))}
    </main>
  );
}

createRoot(document.querySelector("#root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
