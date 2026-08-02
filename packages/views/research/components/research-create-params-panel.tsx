"use client";

import { Button } from "@multica/ui/components/ui/button";
import { Label } from "@multica/ui/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@multica/ui/components/ui/radio-group";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { Slider } from "@multica/ui/components/ui/slider";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  CREATE_LANGUAGES,
  DEPTH_TIERS,
  SOURCE_WEIGHT_KEYS,
  SOURCE_WEIGHT_MAX,
  SOURCE_WEIGHT_MIN,
  type ResearchCreateDepthTier,
  type ResearchCreateLanguage,
  type ResearchCreateParams,
  type ResearchSourceWeightKey,
  clampSourceWeight,
  normalizeCreateParams,
} from "../lib/research-create-params";

function WeightRow({
  weightKey,
  value,
  onChange,
}: {
  weightKey: ResearchSourceWeightKey;
  value: number;
  onChange: (next: number) => void;
}) {
  const { t } = useT("research");
  const label =
    weightKey === "secondary"
      ? t(($) => $.create_params.weight_rows.secondary.label)
      : weightKey === "community"
        ? t(($) => $.create_params.weight_rows.community.label)
        : t(($) => $.create_params.weight_rows.primary.label);
  const hint =
    weightKey === "secondary"
      ? t(($) => $.create_params.weight_rows.secondary.hint)
      : weightKey === "community"
        ? t(($) => $.create_params.weight_rows.community.hint)
        : t(($) => $.create_params.weight_rows.primary.hint);

  return (
    <div className="space-y-2" data-testid={`research-create-weight-${weightKey}`}>
      <div className="flex items-baseline justify-between gap-2">
        <Label className="text-sm font-medium text-foreground">{label}</Label>
        <span className="font-mono text-xs tabular-nums text-muted-foreground">
          {value.toFixed(2)}
        </span>
      </div>
      <Slider
        value={[value]}
        min={SOURCE_WEIGHT_MIN}
        max={SOURCE_WEIGHT_MAX}
        step={0.05}
        onValueChange={(v) => {
          const raw = Array.isArray(v) ? v[0] : v;
          onChange(clampSourceWeight(typeof raw === "number" ? raw : value));
        }}
        aria-label={label}
      />
      <p className="text-[11px] leading-relaxed text-muted-foreground">{hint}</p>
    </div>
  );
}

/**
 * LRM-838 — create-flow params sheet: depth / source weights / language.
 * Narrow: fullscreen; desktop: right sheet.
 */
export function ResearchCreateParamsPanel({
  open,
  value,
  onOpenChange,
  onChange,
}: {
  open: boolean;
  value: ResearchCreateParams;
  onOpenChange: (open: boolean) => void;
  onChange: (next: ResearchCreateParams) => void;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const params = normalizeCreateParams(value);

  const setDepth = (depth_tier: ResearchCreateDepthTier) =>
    onChange({ ...params, depth_tier });
  const setLanguage = (language: ResearchCreateLanguage) =>
    onChange({ ...params, language });
  const setWeight = (key: ResearchSourceWeightKey, next: number) =>
    onChange({
      ...params,
      source_weights: { ...params.source_weights, [key]: next },
    });

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side={isMobile ? "bottom" : "right"}
        showCloseButton
        data-testid="research-create-params-panel"
        className={cn(
          "gap-0 overflow-hidden p-0",
          isMobile
            ? "inset-0 h-dvh max-h-dvh w-full border-0 sm:max-w-none"
            : "w-full sm:max-w-md",
        )}
      >
        <SheetHeader className="shrink-0 border-b px-4 py-3 pr-12 text-left">
          <SheetTitle>{t(($) => $.create_params.title)}</SheetTitle>
          <SheetDescription>{t(($) => $.create_params.hint)}</SheetDescription>
        </SheetHeader>

        <div className="min-h-0 flex-1 space-y-6 overflow-y-auto px-4 py-4">
          <section className="space-y-3" data-testid="research-create-depth">
            <div>
              <h3 className="text-sm font-semibold text-foreground">
                {t(($) => $.create_params.depth_label)}
              </h3>
              <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
                {t(($) => $.create_params.depth_hint)}
              </p>
            </div>
            <RadioGroup
              value={params.depth_tier}
              onValueChange={(v) => {
                if (typeof v === "string" && DEPTH_TIERS.includes(v as ResearchCreateDepthTier)) {
                  setDepth(v as ResearchCreateDepthTier);
                }
              }}
              className="grid gap-2"
            >
              {DEPTH_TIERS.map((tier) => {
                const label =
                  tier === "shallow"
                    ? t(($) => $.create_params.depth_tiers.shallow.label)
                    : tier === "deep"
                      ? t(($) => $.create_params.depth_tiers.deep.label)
                      : t(($) => $.create_params.depth_tiers.standard.label);
                const hint =
                  tier === "shallow"
                    ? t(($) => $.create_params.depth_tiers.shallow.hint)
                    : tier === "deep"
                      ? t(($) => $.create_params.depth_tiers.deep.hint)
                      : t(($) => $.create_params.depth_tiers.standard.hint);
                return (
                  <label
                    key={tier}
                    className={cn(
                      "flex cursor-pointer items-start gap-2.5 rounded-xl border px-3 py-2.5 transition-colors",
                      params.depth_tier === tier
                        ? "border-brand/40 bg-brand/8"
                        : "border-border/70 bg-card/60 hover:bg-muted/40",
                    )}
                  >
                    <RadioGroupItem value={tier} className="mt-0.5" />
                    <span className="min-w-0">
                      <span className="block text-sm font-medium">{label}</span>
                      <span className="mt-0.5 block text-[11px] leading-relaxed text-muted-foreground">
                        {hint}
                      </span>
                    </span>
                  </label>
                );
              })}
            </RadioGroup>
          </section>

          <section className="space-y-4" data-testid="research-create-weights">
            <div>
              <h3 className="text-sm font-semibold text-foreground">
                {t(($) => $.create_params.weights_label)}
              </h3>
              <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
                {t(($) => $.create_params.weights_hint)}
              </p>
            </div>
            {SOURCE_WEIGHT_KEYS.map((key) => (
              <WeightRow
                key={key}
                weightKey={key}
                value={params.source_weights[key]}
                onChange={(next) => setWeight(key, next)}
              />
            ))}
          </section>

          <section className="space-y-3" data-testid="research-create-language">
            <div>
              <h3 className="text-sm font-semibold text-foreground">
                {t(($) => $.create_params.language_label)}
              </h3>
              <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
                {t(($) => $.create_params.language_hint)}
              </p>
            </div>
            <RadioGroup
              value={params.language}
              onValueChange={(v) => {
                if (
                  typeof v === "string" &&
                  CREATE_LANGUAGES.includes(v as ResearchCreateLanguage)
                ) {
                  setLanguage(v as ResearchCreateLanguage);
                }
              }}
              className="grid grid-cols-2 gap-2"
            >
              {CREATE_LANGUAGES.map((lang) => (
                <label
                  key={lang}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-xl border px-3 py-2.5 transition-colors",
                    params.language === lang
                      ? "border-brand/40 bg-brand/8"
                      : "border-border/70 bg-card/60 hover:bg-muted/40",
                  )}
                >
                  <RadioGroupItem value={lang} />
                  <span className="text-sm font-medium">
                    {lang === "zh"
                      ? t(($) => $.create_params.language_options.zh)
                      : t(($) => $.create_params.language_options.en)}
                  </span>
                </label>
              ))}
            </RadioGroup>
          </section>
        </div>

        <div className="shrink-0 border-t px-4 py-3">
          <Button
            type="button"
            className="h-10 w-full rounded-full bg-brand text-brand-foreground hover:bg-brand/90"
            data-testid="research-create-params-done"
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.create_params.done)}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}
