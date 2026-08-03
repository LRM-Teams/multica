"use client";

import { useEffect, useId, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  countHanChars,
  isTemplatePromptAboveMin,
  RESEARCH_TEMPLATE_MIN_HAN,
} from "../lib/research-templates";

type ResearchTemplatePromptEditorProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Default full prompt from composeTemplateGoal. */
  defaultPrompt: string;
  /** Currently applied full prompt (may equal default). */
  value: string;
  onApply: (next: string) => void;
  disabled?: boolean;
};

/**
 * LRM-1139 / LRM-1140 A2: expand/edit full authoritative prompt.
 * Desktop → Dialog; viewport &lt;768 → Sheet. Does not dump into main textarea.
 */
export function ResearchTemplatePromptEditor({
  open,
  onOpenChange,
  defaultPrompt,
  value,
  onApply,
  disabled = false,
}: ResearchTemplatePromptEditorProps) {
  const { t, i18n } = useT("research");
  const isMobile = useIsMobile();
  const errorId = useId();
  const [draft, setDraft] = useState(value);
  const [attempted, setAttempted] = useState(false);
  const isZh = (i18n?.language ?? "en").toLowerCase().startsWith("zh");

  useEffect(() => {
    if (!open) return;
    setDraft(value);
    setAttempted(false);
  }, [open, value]);

  const han = countHanChars(draft);
  const len = draft.trim().length;
  const tooShort = !isTemplatePromptAboveMin(draft, i18n?.language);
  const empty = len === 0;
  const showError = attempted && (empty || tooShort);
  const errorMessage = empty
    ? t(($) => $.home.template_prompt_empty)
    : tooShort
      ? isZh
        ? t(($) => $.home.template_prompt_too_short, {
            min: RESEARCH_TEMPLATE_MIN_HAN,
            count: han,
          })
        : t(($) => $.home.template_prompt_too_short_en, {
            min: 800,
            count: len,
          })
      : null;

  const handleApply = () => {
    setAttempted(true);
    if (empty || tooShort) return;
    onApply(draft);
    onOpenChange(false);
  };

  const handleReset = () => {
    setDraft(defaultPrompt);
    setAttempted(false);
  };

  const body = (
    <>
      <Textarea
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        disabled={disabled}
        rows={16}
        data-testid="research-template-prompt-editor"
        aria-invalid={showError ? true : undefined}
        aria-describedby={showError ? errorId : undefined}
        className="min-h-[240px] flex-1 resize-y font-mono text-[12.5px] leading-relaxed"
      />
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px]">
        <span
          data-testid="research-template-prompt-count"
          className={cn(
            "tabular-nums text-muted-foreground",
            showError && "text-destructive",
          )}
        >
          {isZh
            ? t(($) => $.home.template_prompt_count, {
                count: han,
                min: RESEARCH_TEMPLATE_MIN_HAN,
              })
            : t(($) => $.home.template_prompt_count_en, {
                count: len,
                min: 800,
              })}
        </span>
        {showError && errorMessage ? (
          <p
            id={errorId}
            role="alert"
            data-testid="research-template-prompt-error"
            className="text-destructive"
          >
            {errorMessage}
          </p>
        ) : null}
      </div>
    </>
  );

  const footer = (
    <>
      <Button
        type="button"
        variant="outline"
        disabled={disabled}
        onClick={handleReset}
        data-testid="research-template-prompt-reset"
      >
        {t(($) => $.home.template_prompt_reset)}
      </Button>
      <Button
        type="button"
        variant="outline"
        disabled={disabled}
        onClick={() => onOpenChange(false)}
      >
        {t(($) => $.home.template_prompt_cancel)}
      </Button>
      <Button
        type="button"
        disabled={disabled}
        onClick={handleApply}
        data-testid="research-template-prompt-apply"
      >
        {t(($) => $.home.template_prompt_apply)}
      </Button>
    </>
  );

  if (isMobile) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          side="bottom"
          className="flex h-[min(92dvh,720px)] max-h-[92dvh] w-full flex-col gap-3 sm:max-w-none"
          data-testid="research-template-prompt-sheet"
        >
          <SheetHeader className="shrink-0 px-0 pt-0">
            <SheetTitle>{t(($) => $.home.template_prompt_title)}</SheetTitle>
            <SheetDescription>
              {t(($) => $.home.template_prompt_desc)}
            </SheetDescription>
          </SheetHeader>
          <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden px-4">
            {body}
          </div>
          <SheetFooter className="shrink-0 flex-row flex-wrap justify-end gap-2 border-t">
            {footer}
          </SheetFooter>
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="flex max-h-[min(90dvh,720px)] w-full max-w-[calc(100%-2rem)] flex-col gap-3 sm:max-w-2xl"
        data-testid="research-template-prompt-dialog"
      >
        <DialogHeader>
          <DialogTitle>{t(($) => $.home.template_prompt_title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.home.template_prompt_desc)}
          </DialogDescription>
        </DialogHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
          {body}
        </div>
        <DialogFooter className="sm:justify-end">{footer}</DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
