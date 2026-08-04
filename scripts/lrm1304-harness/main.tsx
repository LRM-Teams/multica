/**
 * LRM-1304 gate-shot harness — channel message-row reading geometry.
 *
 * BEFORE = the landed `dev` markup at `446162646`
 *          (`packages/views/channels/components/channel-message-bubble.tsx`):
 *          permanent fine-pointer right gutter `pr-[136px]` on the bubble shell
 *          + visible 1px shell edges (`border-line` + per-side `border-x/y`).
 * SPEC   = this design gate's proposal: no permanent gutter (body eats the full
 *          row width), no visible shell edge, author row alone reserves the
 *          action-bar band, and continuation rows get a first-line-only float
 *          safe zone so the overlay bar never lands on body text.
 *
 * Every BEFORE class string below is asserted verbatim against the real
 * component source by `scripts/lrm1304-gate-shots.mjs`, so the harness cannot
 * drift into measuring a fiction.
 *
 * Query params:
 *   ?variant=before|spec
 *   ?theme=light|dark
 *
 * Temporary tooling: delete after the shots are attached to LRM-1304.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Copy, MessageSquare, Pencil, Quote, SmilePlus } from "lucide-react";
import "./harness.css";

const params = new URLSearchParams(window.location.search);
const theme = params.get("theme") === "dark" ? "dark" : "light";
const variant = (params.get("variant") || "before") as "before" | "spec";
document.documentElement.classList.add(theme);

/* ------------------------------------------------------------------ *
 * Verbatim strings from dev@446162646 (asserted by the shots script).
 * ------------------------------------------------------------------ */
export const ROW_CLASS =
  "group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 outline-none transition-colors duration-1000";
export const MESSAGE_SHELL_CLASS =
  "px-1 border-line transition-colors group-hover:border-line-strong group-focus-within:border-line-strong";
export const SHELL_GUTTER_CLASS = "[@media(pointer:fine)]:pr-[136px]";
export const BAR_CLASS =
  "pointer-events-none absolute right-2 z-10 hidden items-center gap-0.5 rounded-lg border border-line-strong bg-popover p-0.5 text-muted-foreground opacity-0 shadow-sm transition-opacity [@media(pointer:fine)]:flex [@media(pointer:fine)]:group-hover:pointer-events-auto [@media(pointer:fine)]:group-hover:opacity-100 [@media(pointer:fine)]:group-focus-within:pointer-events-auto [@media(pointer:fine)]:group-focus-within:opacity-100";
export const BODY_CLASS =
  "message-surface relative min-w-0 max-w-full select-text break-words [overflow-wrap:anywhere] text-[13.5px] leading-6 text-foreground";
export const AUTHOR_ROW_CLASS =
  "mb-0.5 flex min-w-0 select-none items-center gap-1.5 text-[13.5px]";
const ICON_BTN =
  "inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

/* ------------------------------------------------------------------ *
 * SPEC deltas.
 *
 * Safe band = measured 5-key bar width (154) + 8px breathing gap = 162;
 * the float is 36px tall because the compact bar spans 2..36px from the body's
 * top edge (`top-0.5` + 34px bar), so a 34px float left a 2px sliver exposed.
 * The float lives inside the body, which the shell's `px-1` already insets by
 * 4px, so the float itself is 158.
 *
 * Literal class strings only: Tailwind v4 scans source text, so an
 * interpolated arbitrary value would silently never be generated.
 * ------------------------------------------------------------------ */
export const SPEC_SAFE_BAND_PX = 162;
export const SPEC_AUTHOR_ROW_PR =
  "[@media(pointer:fine)_and_(min-width:640px)]:pr-[162px]";
export const SPEC_FIRSTLINE_SAFE =
  "before:content-[''] [@media(pointer:fine)_and_(min-width:640px)]:before:float-right [@media(pointer:fine)_and_(min-width:640px)]:before:h-[36px] [@media(pointer:fine)_and_(min-width:640px)]:before:w-[158px]";
/** Card-like first blocks (quote / attachment / code) keep full width and inset
 *  their own content instead, so the bar never sits on a card background. */
export const SPEC_CARD_SAFE = "[@media(pointer:fine)_and_(min-width:640px)]:pr-[158px]";
/**
 * SPEC bar gate: a fine pointer in a window narrower than 640px cannot afford a
 * 162px safe band (measured: it leaves 124px = ~9 CJK glyphs on the first line
 * of a 282px body). Below 640px the row falls back to the affordance coarse
 * pointers already use (long-press / context menu), so the body keeps its full
 * width and there is nothing to dodge.
 */
export const SPEC_BAR_CLASS =
  "pointer-events-none absolute right-2 z-10 hidden items-center gap-0.5 rounded-lg border border-line-strong bg-popover p-0.5 text-muted-foreground opacity-0 shadow-sm transition-opacity [@media(pointer:fine)_and_(min-width:640px)]:flex [@media(pointer:fine)_and_(min-width:640px)]:group-hover:pointer-events-auto [@media(pointer:fine)_and_(min-width:640px)]:group-hover:opacity-100 [@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:pointer-events-auto [@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:opacity-100";

function cx(...parts: (string | false | null | undefined)[]) {
  return parts.filter(Boolean).join(" ");
}

function shellEdge(groupStart: boolean, groupEnd: boolean) {
  if (groupStart && groupEnd) return "rounded-lg border-x border-y";
  if (groupStart) return "rounded-t-lg border-x border-t";
  if (groupEnd) return "rounded-b-lg border-x border-b";
  return "border-x";
}

const LONG =
  "先把断点收口：这条是压力样本，用来验证正文在移除常态右侧留白之后能否吃满整行宽度，同时保证悬浮操作条在任何一行都不落到文字上面，长文要能一直排到行尾再折行，而不是提前 136 像素就换行。";

function ActionBar({ compact, isSpec }: { compact: boolean; isSpec: boolean }) {
  return (
    <div
      data-testid="message-action-bar"
      className={cx(
        isSpec ? SPEC_BAR_CLASS : BAR_CLASS,
        compact ? "top-0.5" : "top-1 -translate-y-1/2",
      )}
    >
      <button type="button" className={ICON_BTN} aria-label="添加表情">
        <SmilePlus className="size-4" />
      </button>
      <button type="button" className={ICON_BTN} aria-label="复制">
        <Copy className="size-4" />
      </button>
      <button type="button" className={ICON_BTN} aria-label="引用">
        <Quote className="size-4" />
      </button>
      <button type="button" className={ICON_BTN} aria-label="在话题中回复">
        <MessageSquare className="size-4" />
      </button>
      <button type="button" className={ICON_BTN} aria-label="编辑">
        <Pencil className="size-4" />
      </button>
    </div>
  );
}

function QuoteCard({ safeBand }: { safeBand: boolean }) {
  return (
    <div
      data-testid="message-quote-card"
      className={cx(
        "mb-1 flex items-start gap-2 rounded-md border border-border/45 bg-muted/25 px-2 py-1",
        safeBand && SPEC_CARD_SAFE,
      )}
    >
      <span className="mt-[3px] h-3.5 w-0.5 shrink-0 rounded-full bg-border" />
      <p className="min-w-0 flex-1 truncate text-xs leading-5 text-muted-foreground">
        <span className="font-medium text-foreground/75">贝克汉姆</span>
        {": "}
        <span>
          聚合树的三档卡片尺寸要先对齐已落地常量，否则前端一定返工，这一句用来测窄屏截断起点。
        </span>
      </p>
    </div>
  );
}

function ReplyPreview() {
  return (
    <div data-testid="thread-reply-preview" className="mt-1">
      <div className="mb-0.5 text-[11px] font-medium text-brand">3 条回复</div>
      <ul className="flex flex-col gap-1">
        {[
          ["罗纳尔多", "轮末判定卡我按 SOP 补了 continue/stop 两态。"],
          ["前端开发·任务与工作区", "1247 已双绿自合，等交付门。"],
        ].map(([name, text]) => (
          <li key={name} className="flex h-6 min-w-0 items-center gap-2">
            <span className="size-[18px] shrink-0 rounded-[4px] bg-muted" />
            <span className="min-w-0 flex-1 truncate text-xs leading-6">
              <span className="font-semibold text-foreground">{name}</span>
              <span className="font-normal text-muted-foreground"> {text}</span>
            </span>
            <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground/60">
              12:04
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

type RowSpec = {
  id: string;
  compact: boolean;
  groupStart: boolean;
  groupEnd: boolean;
  kind: "long" | "short" | "quote" | "replies";
};

const ROWS: RowSpec[] = [
  { id: "lead-long", compact: false, groupStart: true, groupEnd: false, kind: "long" },
  { id: "cont-long", compact: true, groupStart: false, groupEnd: false, kind: "long" },
  { id: "cont-quote", compact: true, groupStart: false, groupEnd: false, kind: "quote" },
  { id: "cont-replies", compact: true, groupStart: false, groupEnd: true, kind: "replies" },
  { id: "lead-short", compact: false, groupStart: true, groupEnd: true, kind: "short" },
];

function Row({ spec }: { spec: RowSpec }) {
  const isSpec = variant === "spec";
  const shell = cx(
    "min-w-0 max-w-full",
    isSpec ? "px-1" : cx(SHELL_GUTTER_CLASS, MESSAGE_SHELL_CLASS, shellEdge(spec.groupStart, spec.groupEnd)),
    "bg-background",
  );
  return (
    <div
      data-testid={`row-${spec.id}`}
      data-compact={spec.compact ? "true" : undefined}
      className={cx(ROW_CLASS, spec.compact ? "py-0" : "py-1", isSpec && spec.groupEnd && "mb-2")}
      tabIndex={-1}
    >
      {spec.compact ? (
        <span className="mt-0.5 self-start pt-0.5 text-[10px] tabular-nums text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100">
          12:0{spec.id.length % 9}
        </span>
      ) : (
        <span className="mt-0.5 size-7 rounded-md bg-muted" />
      )}
      <div data-testid="message-shell" className={shell}>
        {!spec.compact && (
          <div
            data-testid="message-author-row"
            className={cx(AUTHOR_ROW_CLASS, isSpec && SPEC_AUTHOR_ROW_PR)}
          >
            <span className="truncate font-semibold">UI设计·响应式体验</span>
            <span className="inline-flex h-5 shrink-0 items-center text-[10px] leading-none tabular-nums text-muted-foreground/50">
              12:03
            </span>
          </div>
        )}
        <ActionBar compact={spec.compact} isSpec={isSpec} />
        <div
          data-testid="message-body"
          className={cx(BODY_CLASS, isSpec && spec.compact && SPEC_FIRSTLINE_SAFE)}
        >
          {spec.kind === "quote" && <QuoteCard safeBand={isSpec && spec.compact} />}
          <p data-testid="message-text">
            {spec.kind === "short"
              ? "收到，我这边接着做。"
              : spec.kind === "replies"
                ? "这条挂了话题回复，用来看窄屏下预览行的截断起点。"
                : LONG}
          </p>
          {spec.kind === "replies" && <ReplyPreview />}
        </div>
      </div>
    </div>
  );
}

function Harness() {
  return (
    <div data-testid="lrm1304-surface" className="flex h-dvh flex-col bg-background">
      <div className="shrink-0 border-b border-border px-3 py-2 text-[13.5px] font-medium">
        #调研模块开发
        <span className="ml-2 text-[11px] font-normal text-muted-foreground">
          {variant === "before" ? "BEFORE · dev@446162646" : "SPEC · LRM-1304"}
        </span>
      </div>
      <div className="flex-1 overflow-y-auto px-2 py-2 md:px-5">
        {ROWS.map((spec) => (
          <Row key={spec.id} spec={spec} />
        ))}
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Harness />
  </StrictMode>,
);
