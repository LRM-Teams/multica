"use client";

import { useState, type ReactNode } from "react";
import { Check, CreditCard } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
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
 * Workspace Plan & Billing preview. Uses Multica dashboard chrome
 * (PageHeader + Card/Button tokens) rather than a marketing landing look.
 * CTAs are disabled until checkout is wired.
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

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 p-4 md:p-6">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
            <div className="space-y-1">
              <h2 className="text-xl font-semibold tracking-tight">{t(($) => $.title)}</h2>
              <p className="max-w-xl text-sm text-muted-foreground">
                {t(($) => $.subtitle)}
              </p>
            </div>

            <fieldset className="inline-flex self-start rounded-lg bg-muted p-1">
              <legend className="sr-only">{t(($) => $.billing.cycle_label)}</legend>
              <button
                type="button"
                onClick={() => setCycle("monthly")}
                className={cn(
                  "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
                  cycle === "monthly"
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground",
                )}
                aria-pressed={cycle === "monthly"}
              >
                {t(($) => $.billing.monthly)}
              </button>
              <button
                type="button"
                onClick={() => setCycle("yearly")}
                className={cn(
                  "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
                  cycle === "yearly"
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground",
                )}
                aria-pressed={cycle === "yearly"}
              >
                {t(($) => $.billing.yearly)}
                <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
                  {t(($) => $.billing.save_badge)}
                </Badge>
              </button>
            </fieldset>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <PlanCard
              name={t(($) => $.plans.free.name)}
              tagline={t(($) => $.plans.free.tagline)}
              price={
                <span className="text-3xl font-semibold tracking-tight tabular-nums">
                  {t(($) => $.plans.free.price)}
                </span>
              }
              features={FREE_FEATURE_KEYS.map((key) =>
                t(($) => $.plans.free.features[key]),
              )}
              cta={t(($) => $.plans.free.cta)}
              ctaVariant="outline"
            />

            <PlanCard
              highlighted
              badge={t(($) => $.plans.pro.recommended)}
              name={t(($) => $.plans.pro.name)}
              tagline={t(($) => $.plans.pro.tagline)}
              price={
                <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-1">
                  <span className="text-3xl font-semibold tracking-tight tabular-nums">
                    {proPrice}
                  </span>
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
              ctaVariant="default"
            />

            <PlanCard
              name={t(($) => $.plans.enterprise.name)}
              tagline={t(($) => $.plans.enterprise.tagline)}
              price={
                <span className="text-2xl font-semibold tracking-tight text-muted-foreground">
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

          <p className="text-xs text-muted-foreground">{t(($) => $.placeholder_hint)}</p>
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
  highlighted,
  badge,
}: {
  name: string;
  tagline: string;
  price: ReactNode;
  notes?: string[];
  features: string[];
  cta: string;
  ctaVariant: "default" | "outline";
  highlighted?: boolean;
  badge?: string;
}) {
  return (
    <Card
      className={cn(
        "h-full",
        highlighted && "ring-2 ring-primary/40",
      )}
    >
      <CardHeader className="gap-2">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base font-semibold">{name}</CardTitle>
          {badge ? <Badge variant="secondary">{badge}</Badge> : null}
        </div>
        <CardDescription>{tagline}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-4">
        <div>{price}</div>
        {notes && notes.length > 0 ? (
          <div className="space-y-1">
            {notes.map((note) => (
              <p key={note} className="text-xs leading-relaxed text-muted-foreground">
                {note}
              </p>
            ))}
          </div>
        ) : null}
        <ul className="space-y-2.5 border-t border-border/60 pt-4">
          {features.map((feature) => (
            <li key={feature} className="flex items-start gap-2 text-sm leading-snug">
              <Check
                className="mt-0.5 size-4 shrink-0 text-primary"
                aria-hidden
              />
              <span>{feature}</span>
            </li>
          ))}
        </ul>
      </CardContent>
      <CardFooter>
        <Button type="button" variant={ctaVariant} className="w-full" disabled>
          {cta}
        </Button>
      </CardFooter>
    </Card>
  );
}
