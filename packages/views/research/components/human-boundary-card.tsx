"use client";

import type { ReactNode } from "react";
import { AlertCircle, Bot, Loader2, UserRound } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type {
  HumanBoundaryMode,
  HumanBoundaryModel,
} from "../lib/m2-visibility";
import { resolveHumanBoundaryMode } from "../lib/m2-visibility";
import { ResearchPendingRetryButton } from "./research-pending-retry-button";

function BoundaryHeader({
  title,
  term,
  hint,
  chip,
  status,
  embedded,
}: {
  title: string;
  term: string;
  hint: string;
  chip: string;
  status?: string;
  embedded?: boolean;
}) {
  return (
    <header className="mb-2.5 space-y-1">
      <div className="flex flex-wrap items-center gap-1.5">
        <h3
          className={cn(
            "font-semibold tracking-tight text-foreground",
            embedded ? "text-base" : "text-sm",
          )}
        >
          {title}
        </h3>
        <span className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {term}
        </span>
        <span className="rounded-md border border-border/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">
          {chip}
        </span>
      </div>
      <p className="text-[11px] leading-snug text-muted-foreground">{hint}</p>
      {status ? (
        <p
          data-testid="human-boundary-status"
          className="text-[11px] font-medium text-foreground/85"
        >
          {status}
        </p>
      ) : null}
    </header>
  );
}

function FactCard({
  label,
  value,
  tone,
  icon,
}: {
  label: string;
  value: string;
  tone: "ai" | "human" | "neutral";
  icon: ReactNode;
}) {
  return (
    <article
      data-testid="human-boundary-fact"
      className={cn(
        "min-w-0 rounded-xl border bg-card/90 p-3 shadow-sm backdrop-blur-sm",
        tone === "ai" &&
          "border-[color:color-mix(in_oklab,var(--brand)_40%,var(--border))]",
        tone === "human" &&
          "border-[color:color-mix(in_oklab,var(--role-human)_45%,var(--border))]",
        tone === "neutral" && "border-border/60",
      )}
    >
      <div className="mb-1.5 flex items-center gap-1.5">
        <span className="text-muted-foreground" aria-hidden>
          {icon}
        </span>
        <span
          className={cn(
            "text-[10px] font-medium tracking-wide",
            tone === "human" && "text-[color:var(--role-human)]",
            tone === "ai" && "text-brand",
            tone === "neutral" && "text-muted-foreground",
          )}
        >
          {label}
        </span>
      </div>
      <p className="text-[12px] leading-relaxed break-words text-foreground">
        {value}
      </p>
    </article>
  );
}

function ExpectedOutcomes({ items }: { items: string[] }) {
  return (
    <ul
      data-testid="human-boundary-expected"
      className="mt-1 space-y-1.5 rounded-xl border border-border/55 bg-card/70 px-3 py-2.5"
    >
      {items.map((item) => (
        <li
          key={item}
          className="flex gap-2 text-[11px] leading-snug text-muted-foreground"
        >
          <span
            className="mt-1.5 size-1 shrink-0 rounded-full bg-brand/70"
            aria-hidden
          />
          <span>{item}</span>
        </li>
      ))}
    </ul>
  );
}

/**
 * Persistent shell (LRM-1201).
 *
 * The root node and the polite live region must survive every mode swap: a live
 * region that mounts together with its text is not announced, and one that is
 * unmounted when content arrives can never announce completion.
 */
function BoundaryShell({
  mode,
  shell,
  title,
  term,
  hint,
  chip,
  status,
  embedded,
  liveText,
  children,
}: {
  mode: HumanBoundaryMode;
  shell: string;
  title: string;
  term: string;
  hint: string;
  chip: string;
  status?: string;
  embedded?: boolean;
  liveText: string;
  children: ReactNode;
}) {
  return (
    <section
      data-testid="human-boundary-card"
      role={mode === "error" ? "alert" : undefined}
      aria-busy={mode === "loading"}
      className={shell}
    >
      <BoundaryHeader
        title={title}
        term={term}
        hint={hint}
        chip={chip}
        status={status}
        embedded={embedded}
      />
      {/* Error text already lives in role=alert — keep this silent to avoid a double read. */}
      <p data-testid="human-boundary-live" className="sr-only" aria-live="polite">
        {liveText}
      </p>
      {children}
    </section>
  );
}

export function HumanBoundaryCard({
  model,
  sessionStatus,
  error,
  onRetry,
  retryPending = false,
  className,
  embedded = false,
}: {
  model: HumanBoundaryModel;
  /** Session status drives empty vs in-flight loading (LRM-978). */
  sessionStatus?: string | null;
  error?: string | null;
  onRetry?: () => void;
  /** Disable retry while a refetch is in flight (LRM-1282). */
  retryPending?: boolean;
  className?: string;
  /** Report-reader embed (LRM-880 coexist). */
  embedded?: boolean;
}) {
  const { t } = useT("research");
  // Embedded report view keeps content/empty only — no session loading shell.
  const mode: HumanBoundaryMode = embedded
    ? model.empty
      ? "empty"
      : "ready"
    : resolveHumanBoundaryMode(model, sessionStatus, error);

  const shell = cn(
    "relative z-[1]",
    embedded
      ? "rounded-xl border border-border/60 bg-card/95 p-4 shadow-sm backdrop-blur-sm"
      : "rounded-xl border border-border/55 bg-background/55 p-3 shadow-sm backdrop-blur-sm",
    className,
  );

  const title = t(($) => $.m2.boundary_primary_title);
  const term = t(($) => $.m2.boundary_title);
  const hint = t(($) => $.m2.boundary_hint);
  const chip = t(($) => $.m2.boundary_chip);

  const statusText =
    mode === "loading"
      ? t(($) => $.m2.boundary_loading)
      : mode === "partial"
        ? t(($) => $.m2.boundary_partial)
        : mode === "ready"
          ? t(($) => $.m2.boundary_ready_status)
          : undefined;

  const liveText =
    mode === "loading"
      ? t(($) => $.m2.boundary_loading)
      : mode === "partial"
        ? t(($) => $.m2.boundary_partial)
        : mode === "ready"
          ? t(($) => $.m2.boundary_ready_live)
          : mode === "empty"
            ? t(($) => $.m2.boundary_empty_title)
            : "";

  const expected = [
    t(($) => $.m2.boundary_expect_1),
    t(($) => $.m2.boundary_expect_2),
    t(($) => $.m2.boundary_expect_3),
  ];

  const frame = (children: ReactNode) => (
    <BoundaryShell
      mode={mode}
      shell={shell}
      title={title}
      term={term}
      hint={hint}
      chip={chip}
      status={statusText}
      embedded={embedded}
      liveText={liveText}
    >
      {children}
    </BoundaryShell>
  );

  if (mode === "error") {
    return frame(
      <div
        data-testid="human-boundary-error"
        className="flex flex-col items-start gap-2 py-1"
      >
        <AlertCircle className="size-4 text-destructive" aria-hidden />
        <p className="text-sm text-destructive">
          {t(($) => $.m2.boundary_error)}
          {error ? (
            <span className="mt-1 block text-xs text-destructive/90">{error}</span>
          ) : null}
        </p>
        {onRetry ? (
          <ResearchPendingRetryButton
            label={t(($) => $.session_page.retry)}
            pendingLabel={t(($) => $.connectivity.retrying)}
            pending={retryPending}
            onRetry={onRetry}
          />
        ) : null}
      </div>,
    );
  }

  if (mode === "loading") {
    return frame(
      <div data-testid="human-boundary-loading" className="space-y-2">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
          <span>{t(($) => $.m2.boundary_loading)}</span>
        </div>
        <ExpectedOutcomes items={expected} />
      </div>,
    );
  }

  if (mode === "empty") {
    return frame(
      <div data-testid="human-boundary-empty" className="space-y-1">
        <p className="text-sm font-medium text-foreground">
          {t(($) => $.m2.boundary_empty_title)}
        </p>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t(($) => $.m2.boundary_empty_body)}
        </p>
      </div>,
    );
  }

  // ready | partial — only render facts that actually arrived.
  return frame(
    <div
      data-testid="human-boundary-cards"
      className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(15rem,1fr))]"
    >
      {model.aiCeiling ? (
        <FactCard
          label={t(($) => $.m2.ai_ceiling)}
          value={model.aiCeiling}
          tone="ai"
          icon={<Bot className="size-3.5" />}
        />
      ) : null}
      {model.mustHuman ? (
        <FactCard
          label={t(($) => $.m2.must_human)}
          value={model.mustHuman}
          tone="human"
          icon={<UserRound className="size-3.5" />}
        />
      ) : null}
      {model.matrix.length > 0 ? (
        <article
          data-testid="human-boundary-matrix"
          className="min-w-0 overflow-hidden rounded-xl border border-border/60 bg-card/90 shadow-sm backdrop-blur-sm [grid-column:1/-1]"
        >
          <div className="border-b border-border/50 px-3 py-2 text-[10px] font-medium tracking-wide text-muted-foreground">
            {t(($) => $.m2.boundary_matrix_label)}
          </div>
          <ul className="divide-y divide-border/50">
            {model.matrix.map((row) => (
              <li
                key={`${row.human}\0${row.ai}`}
                className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-2 px-3 py-2 text-[11px] leading-snug"
              >
                <div className="min-w-0">
                  <div className="mb-0.5 text-[10px] text-[color:var(--role-human)]">
                    {t(($) => $.m2.col_human)}
                  </div>
                  <p className="break-words text-foreground">{row.human}</p>
                </div>
                <div className="min-w-0">
                  <div className="mb-0.5 text-[10px] text-brand">
                    {t(($) => $.m2.col_ai)}
                  </div>
                  <p className="break-words text-muted-foreground">{row.ai}</p>
                </div>
              </li>
            ))}
          </ul>
        </article>
      ) : null}
    </div>,
  );
}
