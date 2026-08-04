/**
 * LRM-1346 gate-shot harness (temporary tooling; delete once the frames are
 * attached to the issue).
 *
 * Why a harness and not the real route: photographing the shipped shell needs an
 * authenticated `/{workspace}/channels/{id}` session, and this runtime has no
 * reusable login (the same blocker LRM-1335 / LRM-1291 are parked on). What the
 * ① lock actually claims — "no visible edge on the shell or on any group
 * segment" — is a painted-border question, so it is answerable without the
 * server as long as nothing here is hand-written:
 *
 *  - every class string is read out of `channel-message-bubble.tsx` by
 *    `scripts/lrm1346-deborder-shots.mjs` (BEFORE = `origin/dev`, AFTER =
 *    working tree) and handed to this file as data;
 *  - the CSS is the product's own Tailwind + `packages/ui` token build;
 *  - the shots script then measures `getComputedStyle().border*Width` on each
 *    row instead of eyeballing the PNGs.
 *
 * So the frames show the real utilities against the real tokens, and the numbers
 * — not an adjective — carry the verdict.
 */
import { createRoot } from "react-dom/client";
import "./harness.css";
import classes from "./classes.generated.json";

type RowKind = "solo" | "head" | "middle" | "tail";

type Variant = "before" | "after";

const variant: Variant =
  new URLSearchParams(window.location.search).get("variant") === "before" ? "before" : "after";
const set = classes[variant];

const cx = (...parts: Array<string | false | undefined>) => parts.filter(Boolean).join(" ");

/** LRM-1331 geometry that must survive this knife (author row reserve). */
const AUTHOR_ROW_RESERVE = classes.authorRowReserve;

const ROWS: Array<{
  id: RowKind;
  label: string;
  author?: string;
  time: string;
  compact: boolean;
  body: string;
}> = [
  {
    id: "head",
    label: "分组首",
    author: "Alice Display",
    time: "12:03",
    compact: false,
    body: "先把断点统一收口，再谈壳的边——不然每个断点都要重判一次可见性。",
  },
  {
    id: "middle",
    label: "分组中",
    time: "12:03",
    compact: true,
    body: "中段连续消息：原来只画左右两条竖边，现在应当一条都不剩。",
  },
  {
    id: "tail",
    label: "分组末",
    time: "12:04",
    compact: true,
    body: "末段：底边 + 下圆角一起去。",
  },
  {
    id: "solo",
    label: "独立行",
    author: "Bob Display",
    time: "12:06",
    compact: false,
    body: "独立行原来是四边全包的卡片。",
  },
];

function Row({ row }: { row: (typeof ROWS)[number] }) {
  const edge = set.edge[row.id];
  return (
    <div
      data-row={row.id}
      className={cx(
        "group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 outline-none transition-colors duration-1000",
        row.compact ? "py-0" : "py-1",
      )}
      tabIndex={-1}
    >
      {row.compact ? (
        <span className="mt-0.5 select-none self-start justify-self-end pt-0.5 text-[10px] tabular-nums text-muted-foreground opacity-0">
          {row.time}
        </span>
      ) : (
        <div className="mt-0.5 size-7 rounded-md bg-muted" />
      )}
      <div
        data-testid="message-shell"
        data-shell={row.id}
        className={cx("min-w-0 max-w-full", set.shell, edge, "bg-background")}
      >
        {row.author && (
          <div
            className={cx(
              "mb-0.5 flex min-w-0 select-none items-center gap-1.5 text-[13.5px]",
              AUTHOR_ROW_RESERVE,
            )}
          >
            <span className="truncate font-medium text-foreground">{row.author}</span>
            <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
              {row.time}
            </span>
          </div>
        )}
        <div className="message-surface relative min-w-0 max-w-full select-text break-words [overflow-wrap:anywhere] text-[13.5px] leading-6 text-foreground">
          {row.body}
        </div>
      </div>
    </div>
  );
}

function Harness() {
  return (
    <div className="flex h-dvh flex-col bg-background">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 text-[13.5px] font-medium">
        <span>#调研模块开发</span>
        <span data-testid="variant-label" className="text-[11px] text-muted-foreground">
          LRM-1346 · {variant === "before" ? "BEFORE (origin/dev)" : "AFTER (锁①)"}
        </span>
      </div>
      <div className="flex-1 overflow-hidden p-2">
        {ROWS.map((row) => (
          <Row key={row.id} row={row} />
        ))}
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<Harness />);
