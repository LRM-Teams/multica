"use client";

import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n/use-t";

export function NotePeriodBriefIntentConfirm({
  userText,
  onYes,
  onNo,
}: {
  userText: string;
  onYes: () => void;
  onNo: () => void;
}) {
  const { t } = useT("layout");

  return (
    <div className="space-y-4" data-testid="period-brief-intent-confirm">
      <div className="flex justify-end">
        <div className="max-w-[80%] rounded-2xl bg-muted px-3.5 py-2 text-sm break-words">
          {userText}
        </div>
      </div>
      <div className="space-y-2">
        <p className="text-sm leading-relaxed">
          {t(($) => $.notes_page.period_brief_intent_confirm)}
        </p>
        <div className="flex gap-1.5">
          <Button
            type="button"
            size="sm"
            variant="outline"
            data-testid="period-brief-intent-no"
            onClick={onNo}
          >
            {t(($) => $.notes_page.period_brief_intent_no)}
          </Button>
          <Button
            type="button"
            size="sm"
            data-testid="period-brief-intent-yes"
            onClick={onYes}
          >
            {t(($) => $.notes_page.period_brief_intent_yes)}
          </Button>
        </div>
      </div>
    </div>
  );
}
