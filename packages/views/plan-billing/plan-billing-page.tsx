"use client";

import { useState, type ReactNode } from "react";
import { CreditCard } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { PageHeader } from "../layout/page-header";
import { useT } from "../i18n";

type BillingCycle = "monthly" | "yearly";

const FREE_FEATURE_KEYS = [
  "channels",
  "tasks",
  "agents_local",
  "reminders",
  "observability",
  "history",
  "uploads",
] as const;

const PRO_FEATURE_KEYS = [
  "everything_free",
  "unlimited_history",
  "higher_uploads",
  "joint_channels",
  "more_coming",
] as const;

const ENTERPRISE_FEATURE_KEYS = [
  "everything_pro",
  "private_deploy",
  "sso",
  "onboarding",
] as const;

/**
 * Plan & Billing pricing preview. Layout matches the provided mock
 * (title → cycle toggle → three plan cards). CTAs are non-functional for now.
 */
export function PlanBillingPage() {
  const { t } = useT("plan-billing");
  const [cycle, setCycle] = useState<BillingCycle>("yearly");

  const proPrice =
    cycle === "yearly"
      ? t(($) => $.plans.pro.price_yearly)
      : t(($) => $.plans.pro.price_monthly);
  const proBillingNote =
    cycle === "yearly"
      ? t(($) => $.plans.pro.billed_yearly)
      : t(($) => $.plans.pro.billed_monthly);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader className="gap-2">
        <CreditCard className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-sm font-medium">{t(($) => $.navLabel)}</h1>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto bg-[#f7f3eb]">
        <div className="mx-auto flex w-full max-w-6xl flex-col items-center px-4 py-10 md:px-8 md:py-14">
          <h2 className="text-center text-4xl font-bold tracking-tight text-foreground md:text-5xl">
            {t(($) => $.title)}
          </h2>
          <p className="mt-3 text-center text-base text-muted-foreground md:text-lg">
            {t(($) => $.subtitle)}
          </p>

          <div
            className="mt-8 inline-flex items-center rounded-md border-2 border-foreground/90 bg-background p-1"
            role="group"
            aria-label={t(($) => $.billing.cycle_label)}
          >
            <button
              type="button"
              onClick={() => setCycle("monthly")}
              className={cn(
                "rounded px-4 py-1.5 text-sm font-medium transition-colors",
                cycle === "monthly"
                  ? "bg-[#f5d76e] text-foreground"
                  : "bg-transparent text-foreground hover:bg-muted/60",
              )}
              aria-pressed={cycle === "monthly"}
            >
              {t(($) => $.billing.monthly)}
            </button>
            <button
              type="button"
              onClick={() => setCycle("yearly")}
              className={cn(
                "relative rounded px-4 py-1.5 text-sm font-medium transition-colors",
                cycle === "yearly"
                  ? "bg-[#f5d76e] text-foreground"
                  : "bg-transparent text-foreground hover:bg-muted/60",
              )}
              aria-pressed={cycle === "yearly"}
            >
              {t(($) => $.billing.yearly)}
              <span className="ml-2 align-middle text-[10px] font-bold tracking-wide text-foreground/80">
                {t(($) => $.billing.save_badge)}
              </span>
            </button>
          </div>

          <div className="mt-10 grid w-full gap-5 md:grid-cols-3">
            <PlanCard
              name={t(($) => $.plans.free.name)}
              tagline={t(($) => $.plans.free.tagline)}
              price={
                <span className="text-5xl font-bold tracking-tight">
                  {t(($) => $.plans.free.price)}
                </span>
              }
              features={FREE_FEATURE_KEYS.map((key) =>
                t(($) => $.plans.free.features[key]),
              )}
              cta={t(($) => $.plans.free.cta)}
              ctaVariant="primary"
            />

            <PlanCard
              name={t(($) => $.plans.pro.name)}
              tagline={t(($) => $.plans.pro.tagline)}
              price={
                <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                  <span className="text-5xl font-bold tracking-tight">{proPrice}</span>
                  <span className="text-sm text-muted-foreground">
                    {t(($) => $.plans.pro.price_suffix)}
                  </span>
                </div>
              }
              notes={[
                proBillingNote,
                t(($) => $.plans.pro.seat_hint),
                t(($) => $.plans.pro.example),
              ]}
              features={PRO_FEATURE_KEYS.map((key) =>
                t(($) => $.plans.pro.features[key]),
              )}
              cta={t(($) => $.plans.pro.cta)}
              ctaVariant="primary"
            />

            <PlanCard
              name={t(($) => $.plans.enterprise.name)}
              tagline={t(($) => $.plans.enterprise.tagline)}
              price={
                <span className="text-4xl font-bold tracking-tight text-muted-foreground md:text-5xl">
                  {t(($) => $.plans.enterprise.price)}
                </span>
              }
              features={ENTERPRISE_FEATURE_KEYS.map((key) =>
                t(($) => $.plans.enterprise.features[key]),
              )}
              cta={t(($) => $.plans.enterprise.cta)}
              ctaVariant="outline"
            />
          </div>

          <p className="mt-8 text-center text-xs text-muted-foreground">
            {t(($) => $.placeholder_hint)}
          </p>
        </div>
      </div>
    </div>
  );
}

function PlanCard({
  name,
  tagline,
  price,
  notes,
  features,
  cta,
  ctaVariant,
}: {
  name: string;
  tagline: string;
  price: ReactNode;
  notes?: string[];
  features: string[];
  cta: string;
  ctaVariant: "primary" | "outline";
}) {
  return (
    <section className="flex h-full flex-col rounded-sm border-2 border-foreground/90 bg-background p-6 shadow-[4px_4px_0_0_rgba(0,0,0,0.12)]">
      <h3 className="text-2xl font-bold tracking-tight">{name}</h3>
      <p className="mt-2 min-h-10 text-sm text-muted-foreground">{tagline}</p>
      <div className="mt-6">{price}</div>
      {notes && notes.length > 0 && (
        <div className="mt-2 space-y-1">
          {notes.map((note) => (
            <p key={note} className="text-xs text-muted-foreground">
              {note}
            </p>
          ))}
        </div>
      )}
      <ul className="mt-6 flex-1 space-y-2.5">
        {features.map((feature) => (
          <li key={feature} className="flex items-start gap-2.5 text-sm leading-snug">
            <span className="mt-1.5 size-1.5 shrink-0 bg-foreground" aria-hidden />
            <span>{feature}</span>
          </li>
        ))}
      </ul>
      <button
        type="button"
        disabled
        title={cta}
        className={cn(
          "mt-8 w-full rounded-sm px-4 py-2.5 text-sm font-semibold transition-opacity",
          ctaVariant === "primary"
            ? "bg-[#f472b6] text-white opacity-90"
            : "border-2 border-foreground/90 bg-background text-foreground opacity-90",
        )}
      >
        {cta}
      </button>
    </section>
  );
}
