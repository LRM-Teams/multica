/**
 * LRM-1304 gate-shot harness — channel message-row reading geometry.
 *
 * BEFORE   = the landed `dev` markup at `446162646`
 *            (`packages/views/channels/components/channel-message-bubble.tsx`):
 *            permanent fine-pointer right gutter `pr-[136px]` on the bubble shell
 *            + visible 1px shell edges (`border-line` + per-side `border-x/y`).
 * SPEC     = geometry proposal (gutter + overlap), locked as LRM-1331.
 * GPRIME   = 描边全去（Frank ① lock）+ grouping carried by rhythm, group head and
 *            a row-level hover wash (`--hover`, the token the members list and
 *            add-people dialog already use).
 * GSECOND  = same as GPRIME but **without** the hover wash: hover shows only the
 *            overlay bar and the gutter timestamp. Kept so the cost of dropping
 *            the wash is measurable side by side, not argued.
 *
 * Every BEFORE class string below is asserted verbatim against the real
 * component source by the shots scripts, so the harness cannot drift into
 * measuring a fiction.
 *
 * Query params:
 *   ?variant=before|spec|gprime|gsecond
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
type Variant = "before" | "spec" | "gprime" | "gsecond" | "gprimeslow";
const variant = (params.get("variant") || "before") as Variant;
document.documentElement.classList.add(theme);

/** GPRIME / GPRIMESLOW / GSECOND share every geometry delta; they differ in the wash. */
const isBorderless = variant === "gprime" || variant === "gsecond" || variant === "gprimeslow";
/** Both wash candidates; `gprimeslow` keeps the shipped 1s row transition as a control. */
const hasWash = variant === "gprime" || variant === "gprimeslow";
const isSpec = variant === "spec";
/** No permanent right gutter / no shell edge in SPEC and in both borderless variants. */
const noGutter = isSpec || isBorderless;

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
/** From `packages/views/common/mention-token.ts` — proves a row-level hover wash
 *  is already the shipped language for the one row state that has one. */
export const SELF_MENTION_ROW_CLASS =
  "bg-[#fef9e8] hover:bg-[#fdf3d0] focus-within:bg-[#fdf3d0] dark:border-l-2 dark:border-brand dark:bg-brand/[0.06] dark:hover:bg-brand/[0.08] dark:focus-within:bg-brand/[0.08]";
const ICON_BTN =
  "inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

/* ------------------------------------------------------------------ *
 * SPEC deltas (geometry — already split to LRM-1331).
 * ------------------------------------------------------------------ */
export const SPEC_SAFE_BAND_PX = 162;
export const SPEC_AUTHOR_ROW_PR =
  "[@media(pointer:fine)_and_(min-width:640px)]:pr-[162px]";
export const SPEC_FIRSTLINE_SAFE =
  "before:content-[''] [@media(pointer:fine)_and_(min-width:640px)]:before:float-right [@media(pointer:fine)_and_(min-width:640px)]:before:h-[36px] [@media(pointer:fine)_and_(min-width:640px)]:before:w-[158px]";
export const SPEC_CARD_SAFE = "[@media(pointer:fine)_and_(min-width:640px)]:pr-[158px]";
export const SPEC_BAR_CLASS =
  "pointer-events-none absolute right-2 z-10 hidden items-center gap-0.5 rounded-lg border border-line-strong bg-popover p-0.5 text-muted-foreground opacity-0 shadow-sm transition-opacity [@media(pointer:fine)_and_(min-width:640px)]:flex [@media(pointer:fine)_and_(min-width:640px)]:group-hover:pointer-events-auto [@media(pointer:fine)_and_(min-width:640px)]:group-hover:opacity-100 [@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:pointer-events-auto [@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:opacity-100";

/* ------------------------------------------------------------------ *
 * G′ deltas — what replaces the deleted shell edge.
 *
 * 1. SHELL: `px-1` only. No `border-*`, no `rounded-*`, no per-side segment
 *    class. There is nothing left to strengthen on hover, which is why 2 exists.
 * 2. RHYTHM: the group boundary becomes whitespace. Group head keeps 8px above
 *    it, and only 2px below, so intra-group rows stay welded (0–2px) while the
 *    boundary opens to 8px — a 4:1 ratio, the same signal Slack uses with no
 *    lines at all. Padding lives on the head because `groupStart = !compact` is
 *    always true for a head, while `groupEnd` depends on the next item.
 * 3. WASH (G′ only): `--hover` at row level replaces `border-line-strong`.
 *    Existing language: `hover:bg-hover` in channel-members-list,
 *    channel-add-people-dialog, settings/members-tab; self-mention rows already
 *    hover-wash at row level. Resting surface stays `bg-background` — this is
 *    NOT the `bg-muted` slab Frank kicked back (that was a permanent fill).
 * ------------------------------------------------------------------ */
export const GPRIME_SHELL_CLASS = "px-1";
export const GPRIME_LEAD_RHYTHM = "pt-2 pb-0.5";
export const GPRIME_ROW_HOVER = "hover:bg-hover focus-within:bg-hover";
/**
 * 4. TIMING: `ROW_CLASS` ships `transition-colors duration-1000` — that 1s ramp
 *    exists for the deep-link highlight fade, but a hover wash on the same
 *    property inherits it, so the row is still ~10% washed 200ms after the
 *    pointer arrives (measured). G′ swaps the single duration utility to 100ms;
 *    the shipped 1s value is kept in the `gprimeslow` control frame so the cost
 *    is visible instead of asserted.
 */
export const GPRIME_ROW_CLASS = ROW_CLASS.replace("duration-1000", "duration-100");

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

function ActionBar({ compact }: { compact: boolean }) {
  return (
    <div
      data-testid="message-action-bar"
      className={cx(
        noGutter ? SPEC_BAR_CLASS : BAR_CLASS,
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

/** The one horizontal rule G′ keeps: it marks a real boundary (local day change).
 *  Verbatim from `channel-message-list.tsx`. */
function DateDivider({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 px-5 py-2" data-testid="date-divider">
      <div aria-hidden className="h-px min-w-4 flex-1 bg-border/60" />
      <span className="rounded-full border border-border/60 bg-background/90 px-2.5 py-0.5 text-[11px] font-medium text-muted-foreground shadow-sm">
        {label}
      </span>
      <div aria-hidden className="h-px min-w-4 flex-1 bg-border/60" />
    </div>
  );
}

type RowSpec = {
  id: string;
  /** Group index — the shots script derives "is this a group boundary?" from it. */
  group: number;
  author: string;
  time: string;
  compact: boolean;
  groupStart: boolean;
  groupEnd: boolean;
  selfMentioned?: boolean;
  kind: "long" | "short" | "quote" | "replies" | "mention";
};

const A = "UI设计·响应式体验";
const B = "贝克汉姆";

const ROWS: RowSpec[] = [
  // group 1 — four rows, the welded case
  { id: "lead-long", group: 1, author: A, time: "12:03", compact: false, groupStart: true, groupEnd: false, kind: "long" },
  { id: "cont-long", group: 1, author: A, time: "12:03", compact: true, groupStart: false, groupEnd: false, kind: "long" },
  { id: "cont-quote", group: 1, author: A, time: "12:04", compact: true, groupStart: false, groupEnd: false, kind: "quote" },
  { id: "cont-replies", group: 1, author: A, time: "12:04", compact: true, groupStart: false, groupEnd: true, kind: "replies" },
  // group 2 — single-message group by another author (the boundary case)
  { id: "lead-short", group: 2, author: B, time: "12:06", compact: false, groupStart: true, groupEnd: true, kind: "short" },
  // group 3 — same author as group 2, new group after the day boundary
  { id: "lead-day2", group: 3, author: B, time: "09:12", compact: false, groupStart: true, groupEnd: false, kind: "long" },
  { id: "cont-day2", group: 3, author: B, time: "09:12", compact: true, groupStart: false, groupEnd: true, kind: "short" },
  // group 4 — self-mention wash still has to read as one row without any edge
  { id: "lead-mention", group: 4, author: A, time: "09:20", compact: false, groupStart: true, groupEnd: true, selfMentioned: true, kind: "mention" },
];

/** Date divider sits before this row id (local day change). */
const DIVIDER_BEFORE = "lead-day2";

function Row({ spec }: { spec: RowSpec }) {
  const shell = cx(
    "min-w-0 max-w-full",
    isBorderless
      ? GPRIME_SHELL_CLASS
      : isSpec
        ? "px-1"
        : cx(SHELL_GUTTER_CLASS, MESSAGE_SHELL_CLASS, shellEdge(spec.groupStart, spec.groupEnd)),
    !spec.selfMentioned && "bg-background",
  );
  const rhythm = isBorderless
    ? spec.compact
      ? "py-0"
      : GPRIME_LEAD_RHYTHM
    : cx(spec.compact ? "py-0" : "py-1", isSpec && spec.groupEnd && "mb-2");
  return (
    <div
      data-testid={`row-${spec.id}`}
      data-group={spec.group}
      data-compact={spec.compact ? "true" : undefined}
      className={cx(
        variant === "gprime" ? GPRIME_ROW_CLASS : ROW_CLASS,
        rhythm,
        // The wash that replaces the strengthened edge (G′ / control only).
        hasWash && !spec.selfMentioned && GPRIME_ROW_HOVER,
        spec.selfMentioned && SELF_MENTION_ROW_CLASS,
      )}
      tabIndex={-1}
    >
      {spec.compact ? (
        <span
          data-testid="message-gutter-time"
          className="mt-0.5 self-start pt-0.5 text-[10px] tabular-nums text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100"
        >
          {spec.time}
        </span>
      ) : (
        <span className="mt-0.5 size-7 rounded-md bg-muted" />
      )}
      <div data-testid="message-shell" className={shell}>
        {!spec.compact && (
          <div
            data-testid="message-author-row"
            className={cx(AUTHOR_ROW_CLASS, noGutter && SPEC_AUTHOR_ROW_PR)}
          >
            <span className="truncate font-semibold">{spec.author}</span>
            <span className="inline-flex h-5 shrink-0 items-center text-[10px] leading-none tabular-nums text-muted-foreground/50">
              {spec.time}
            </span>
          </div>
        )}
        <ActionBar compact={spec.compact} />
        <div
          data-testid="message-body"
          className={cx(BODY_CLASS, noGutter && spec.compact && SPEC_FIRSTLINE_SAFE)}
        >
          {spec.kind === "quote" && <QuoteCard safeBand={noGutter && spec.compact} />}
          <p data-testid="message-text">
            {spec.kind === "short"
              ? "收到，我这边接着做。"
              : spec.kind === "replies"
                ? "这条挂了话题回复，用来看窄屏下预览行的截断起点。"
                : spec.kind === "mention"
                  ? "@我 这条是被点名行，去掉壳描边之后它的底色仍然要独立成块。"
                  : LONG}
          </p>
          {spec.kind === "replies" && <ReplyPreview />}
        </div>
      </div>
    </div>
  );
}

const LABEL: Record<Variant, string> = {
  before: "BEFORE · dev@446162646（壳描边 + 常态右侧留白）",
  spec: "SPEC · LRM-1304 几何",
  gprime: "G′ · 无描边：节奏分组 + hover wash（100ms）",
  gsecond: "G″ · 无描边：节奏分组，无 hover wash",
  gprimeslow: "控制组 · G′ 的 wash 但沿用 1s 行过渡",
};

function Harness() {
  return (
    <div data-testid="lrm1304-surface" className="flex h-dvh flex-col bg-background">
      <div className="shrink-0 border-b border-border px-3 py-2 text-[13.5px] font-medium">
        #调研模块开发
        <span className="ml-2 text-[11px] font-normal text-muted-foreground">
          {LABEL[variant]}
        </span>
      </div>
      <div className="flex-1 overflow-y-auto px-2 py-2 md:px-5">
        {ROWS.map((spec) => (
          <div key={spec.id} className="contents">
            {spec.id === DIVIDER_BEFORE && <DateDivider label="8月4日 星期二" />}
            <Row spec={spec} />
          </div>
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
