/**
 * LRM-1228 gate-shot harness (temporary tooling; delete after the shots are
 * attached to the issue).
 *
 * Why a harness and not Vitest: the whole slice is geometry — a 36px in-chip
 * button vs a 20px overflow-corner one, how much filename width that returns,
 * and whether the outdented button lands on the text. jsdom has no layout, so
 * Vitest can only assert the class contract; the numbers need a real browser at
 * a real 360px viewport.
 *
 * Every class string below is asserted against the real component source by
 * `scripts/lrm1228-gate-shots.mjs` (AFTER === working tree, BEFORE ===
 * origin/dev), so this harness cannot drift into measuring a fiction.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { FileIcon, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import "./harness.css";

// ---- fragments lifted verbatim from composer-attachment-tray.tsx ----------
// Unchanged by this slice (shared by both gates).
export const TRAY_CLASS =
  "m-0 flex list-none flex-row flex-nowrap items-center gap-3 overflow-x-auto overflow-y-hidden overscroll-x-contain p-0 pb-0.5 -mt-2 pr-2 pt-2 touch-pan-x [-webkit-overflow-scrolling:touch] [scrollbar-width:thin]";
export const CHIP_BASE_CLASS =
  "group relative flex w-fit max-w-[11rem] shrink-0 list-none flex-row items-center gap-1.5 rounded-lg border border-border/50 bg-muted/35";
// isMobile === true → `h-14` outer height (matches the 56px image thumb).
export const CHIP_H_CLASS = "h-14";

// origin/dev — remove sat inside the chip at `size-9` (36px) on mobile web.
export const BEFORE_CHIP_PAD_CLASS = "min-w-0 px-2";
export const BEFORE_REMOVE_HOLDER_CLASS = "flex shrink-0 items-center gap-0.5";
export const BEFORE_REMOVE_CLASS =
  "shadow-sm size-9 bg-transparent hover:bg-background/80";
export const BEFORE_X_CLASS = "size-3.5";

// This branch — the LRM-1180 corner rule now covers file/stale chips too.
export const AFTER_CHIP_PAD_CLASS = "min-w-0 pl-2 pr-3";
export const AFTER_REMOVE_HOLDER_CLASS =
  "absolute -right-2 -top-2 z-30 flex shrink-0 items-center";
export const AFTER_REMOVE_CLASS =
  'relative size-5 rounded-full border border-border bg-background/95 shadow-sm after:absolute after:-inset-0.5 after:content-[""] opacity-100';
export const AFTER_X_CLASS = "size-3";

const gate =
  new URLSearchParams(window.location.search).get("gate") === "before"
    ? "before"
    : "after";

const isBefore = gate === "before";
const chipPad = isBefore ? BEFORE_CHIP_PAD_CLASS : AFTER_CHIP_PAD_CLASS;
const holderClass = isBefore
  ? BEFORE_REMOVE_HOLDER_CLASS
  : AFTER_REMOVE_HOLDER_CLASS;
const removeClass = isBefore ? BEFORE_REMOVE_CLASS : AFTER_REMOVE_CLASS;
const xClass = isBefore ? BEFORE_X_CLASS : AFTER_X_CLASS;

type Chip = {
  id: string;
  filename: string;
  sub?: string;
};

// A long name is the honest case: `truncate` means the filename always reaches
// the chip's right padding, so it is exactly what an outdented button can cover.
const chips: Chip[] = [
  { id: "file", filename: "research-delivery-report-final.pdf" },
  {
    id: "stale",
    filename: "pasted-screenshot-2026-08-03.png",
    sub: "需重新选择",
  },
];

function Harness() {
  return (
    <div className="flex min-h-dvh flex-col bg-background">
      <div className="border-b border-border px-3 py-2 text-[13.5px] font-medium">
        #调研模块开发
      </div>
      <div className="flex-1" />
      {/* Composer shell: the tray lives directly above the editor row, which is
          why the reserved `pt-2` corner and `-mt-2` giveback matter. */}
      <div className="border-t border-border p-2" data-testid="composer-shell">
        <ul className={TRAY_CLASS} data-testid="composer-attachment-tray" data-gate={gate}>
          {chips.map((chip) => (
            <li
              key={chip.id}
              data-testid={`composer-tray-item-${chip.id}`}
              className={`${CHIP_BASE_CLASS} ${CHIP_H_CLASS} ${chipPad}`}
            >
              <FileIcon
                className="size-3.5 shrink-0 text-muted-foreground"
                aria-hidden
              />
              <div className="min-w-0 flex-1">
                <p
                  data-testid={`composer-tray-name-${chip.id}`}
                  className="truncate text-xs font-medium leading-tight text-foreground"
                  title={chip.filename}
                >
                  {chip.filename}
                </p>
                {chip.sub ? (
                  <p className="truncate text-[10px] leading-tight text-muted-foreground">
                    {chip.sub}
                  </p>
                ) : null}
              </div>
              <div className={holderClass}>
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  className={removeClass}
                  aria-label={`移除 ${chip.filename}`}
                  data-testid={`composer-tray-remove-${chip.id}`}
                >
                  <X className={xClass} />
                </Button>
              </div>
            </li>
          ))}
        </ul>
        <div className="mt-2 rounded-lg border border-border px-3 py-2 text-[13.5px] text-muted-foreground">
          发送消息…
        </div>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Harness />
  </StrictMode>,
);
