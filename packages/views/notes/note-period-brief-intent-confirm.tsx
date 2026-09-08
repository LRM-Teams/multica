"use client";

import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n/use-t";

export function NotePeriodBriefIntentConfirm({
  onConfirm,
  onDecline,
}: {
  onConfirm: () => void;
  onDecline: () => void;
}) {
  const { t } = useT("layout");

  return (
    <div
      className="mx-3 mb-2 space-y-3 rounded-xl border bg-card px-3 py-3"
      data-testid="period-brief-intent-confirm"
    >
      <p className="text-[11px] leading-4 text-muted-foreground">
        {t(($) => $.notes_page.period_brief_intent_confirm_hint)}
      </p>
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          data-testid="period-brief-intent-no"
          onClick={onDecline}
        >
          {t(($) => $.notes_page.period_brief_intent_no)}
        </Button>
        <Button
          type="button"
          size="sm"
          data-testid="period-brief-intent-yes"
          onClick={onConfirm}
        >
          {t(($) => $.notes_page.period_brief_intent_yes)}
        </Button>
      </div>
    </div>
  );
}
