"use client";

import type { ReactNode } from "react";
import { AlertCircle, Bot, Loader2, UserRound } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { HumanBoundaryModel } from "../lib/m2-visibility";
import { resolveHumanBoundaryMode } from "../lib/m2-visibility";

function BoundaryHeader({
  title,
  hint,
  chip,
  embedded,
}: {
  title: string;
  hint: string;
  chip: string;
  embedded?: boolean;
}) {
  return (
    <header className="mb-2.5">
      <div className="flex flex-wrap items-center gap-1.5">
        <h3
          className={cn(
            "font-semibold tracking-wide text-foreground uppercase",
            embedded ? "text-sm" : "text-xs",
          )}
        >
          {title}
        </h3>
        <span className="rounded-md border border-border/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">
          {chip}
        </span>
      </div>
      <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">{hint}</p>
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
        "rounded-xl border bg-card/90 p-3 shadow-sm backdrop-blur-sm",
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
            "text-[10px] font-medium tracking-wide uppercase",
            tone === "human" && "text-[color:var(--role-human)]",
            tone === "ai" && "text-brand",
            tone === "neutral" && "text-muted-foreground",
          )}
        >
          {label}
        </span>
      </div>
      <p className="text-[12px] leading-relaxed text-foreground">{value}</p>
    </article>
  );
}

export function HumanBoundaryCard({
  model,
  sessionStatus,
  error,
  onRetry,
  className,
  embedded = false,
}: {
  model: HumanBoundaryModel;
  /** Session status drives empty vs in-flight loading (LRM-978). */
  sessionStatus?: string | null;
  error?: string | null;
  onRetry?: () => void;
  className?: string;
  /** Report-reader embed (LRM-880 coexist). */
  embedded?: boolean;
}) {
  const { t } = useT("research");
  // Embedded report view keeps content/empty only — no session loading shell.
  const mode = embedded
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

  if (mode === "error") {
    return (
      <section
        data-testid="human-boundary-card"
        role="alert"
        className={shell}
      >
        <BoundaryHeader
          title={t(($) => $.m2.boundary_title)}
          hint={t(($) => $.m2.boundary_hint)}
          chip={t(($) => $.m2.boundary_chip)}
          embedded={embedded}
        />
        <div
          data-testid="human-boundary-error"
          className="flex flex-col items-start gap-2 py-1"
        >
          <AlertCircle className="size-4 text-destructive" aria-hidden />
          <p className="text-sm text-destructive">
            {error || t(($) => $.m2.boundary_error)}
          </p>
          {onRetry ? (
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
              {t(($) => $.session_page.retry)}
            </Button>
          ) : null}
        </div>
      </section>
    );
  }

  if (mode === "loading") {
    return (
      <section
        data-testid="human-boundary-card"
        className={shell}
        aria-busy
        aria-live="polite"
      >
        <BoundaryHeader
          title={t(($) => $.m2.boundary_title)}
          hint={t(($) => $.m2.boundary_hint)}
          chip={t(($) => $.m2.boundary_chip)}
          embedded={embedded}
        />
        <div data-testid="human-boundary-loading" className="space-y-2">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
            <span>{t(($) => $.m2.boundary_loading)}</span>
          </div>
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-3"
              style={{ animationDelay: `${i * 80}ms` }}
            >
              <div className="mb-2 h-2.5 w-[36%] rounded bg-muted/70" />
              <div className="h-2.5 w-full rounded bg-muted/45" />
            </div>
          ))}
        </div>
      </section>
    );
  }

  if (mode === "empty") {
    return (
      <section data-testid="human-boundary-card" className={shell}>
        <BoundaryHeader
          title={t(($) => $.m2.boundary_title)}
          hint={t(($) => $.m2.boundary_hint)}
          chip={t(($) => $.m2.boundary_chip)}
          embedded={embedded}
        />
        <div data-testid="human-boundary-empty" className="space-y-1">
          <p className="text-sm font-medium text-foreground">
            {t(($) => $.m2.boundary_empty_title)}
          </p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.m2.boundary_empty_body)}
          </p>
        </div>
      </section>
    );
  }

  return (
    <section data-testid="human-boundary-card" className={shell}>
      <BoundaryHeader
        title={t(($) => $.m2.boundary_title)}
        hint={t(($) => $.m2.boundary_hint)}
        chip={t(($) => $.m2.boundary_chip)}
        embedded={embedded}
      />
      <div data-testid="human-boundary-cards" className="space-y-2">
        <FactCard
          label={t(($) => $.m2.ai_ceiling)}
          value={model.aiCeiling || t(($) => $.m2.boundary_summary_pending)}
          tone="ai"
          icon={<Bot className="size-3.5" />}
        />
        <FactCard
          label={t(($) => $.m2.must_human)}
          value={model.mustHuman || t(($) => $.m2.boundary_summary_pending)}
          tone="human"
          icon={<UserRound className="size-3.5" />}
        />
        {model.matrix.length > 0 ? (
          <article
            data-testid="human-boundary-matrix"
            className="overflow-hidden rounded-xl border border-border/60 bg-card/90 shadow-sm backdrop-blur-sm"
          >
            <div className="border-b border-border/50 px-3 py-2 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
              {t(($) => $.m2.boundary_matrix_label)}
            </div>
            <ul className="divide-y divide-border/50">
              {model.matrix.map((row) => (
                <li
                  key={`${row.human}\0${row.ai}`}
                  className="grid grid-cols-2 gap-2 px-3 py-2 text-[11px] leading-snug"
                >
                  <div>
                    <div className="mb-0.5 text-[10px] text-[color:var(--role-human)]">
                      {t(($) => $.m2.col_human)}
                    </div>
                    <p className="text-foreground">{row.human}</p>
                  </div>
                  <div>
                    <div className="mb-0.5 text-[10px] text-brand">
                      {t(($) => $.m2.col_ai)}
                    </div>
                    <p className="text-muted-foreground">{row.ai}</p>
                  </div>
                </li>
              ))}
            </ul>
          </article>
        ) : null}
      </div>
    </section>
  );
}
