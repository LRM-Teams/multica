"use client";

import { useState } from "react";
import type { ResearchClarificationQuestion } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ClarificationResolution } from "../lib/clarification-question";

/** LRM-822 — structured option / form answer controls for agent clarification. */
export function ResearchClarificationCard({
  question,
  resolution,
  pending,
  onSelectOption,
  onSubmitForm,
  onSkip,
}: {
  question: ResearchClarificationQuestion;
  resolution: ClarificationResolution;
  pending?: boolean;
  onSelectOption?: (optionId: string) => void;
  onSubmitForm?: (values: Record<string, string>) => void;
  onSkip?: () => void;
}) {
  const { t } = useT("research");
  const [draft, setDraft] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const field of question.fields) init[field.id] = "";
    return init;
  });
  const [localError, setLocalError] = useState<string | null>(null);

  const resolved = resolution.status !== "pending";
  const interactive = !resolved && !pending;
  const binary = question.layout === "binary";
  const isForm = question.layout === "form";

  const selectedOptionId =
    resolution.status === "answered" ? resolution.optionId : undefined;

  const onFormSubmit = () => {
    if (!interactive) return;
    const values: Record<string, string> = {};
    for (const field of question.fields) {
      const value = (draft[field.id] ?? "").trim();
      if (field.required && !value) {
        setLocalError(t(($) => $.clarification.required_fields));
        return;
      }
      values[field.id] = value;
    }
    const hasAny = Object.values(values).some((v) => v.trim());
    if (!hasAny) {
      setLocalError(t(($) => $.clarification.required_fields));
      return;
    }
    setLocalError(null);
    onSubmitForm?.(values);
  };

  return (
    <div
      className="mt-1.5 w-full min-w-0 max-w-full rounded-lg border border-border/80 bg-muted/30 p-2.5"
      data-testid="research-clarification-card"
      data-layout={question.layout}
      data-status={resolution.status}
      data-question-id={question.question_id}
    >
      {question.prompt ? (
        <p className="mb-2 text-sm font-medium text-foreground">{question.prompt}</p>
      ) : null}

      {isForm ? (
        <div className="flex w-full flex-col gap-2" data-testid="research-clarification-form">
          {question.fields.map((field) => (
            <label key={field.id} className="flex w-full flex-col gap-1">
              <span className="text-xs font-medium text-foreground/90">
                {field.label}
                {field.required ? (
                  <span className="text-destructive" aria-hidden>
                    {" "}
                    *
                  </span>
                ) : null}
              </span>
              {field.type === "textarea" ? (
                <Textarea
                  value={draft[field.id] ?? ""}
                  disabled={!interactive}
                  placeholder={field.placeholder}
                  rows={3}
                  className="w-full min-h-[4.5rem] resize-y text-sm"
                  onChange={(e) =>
                    setDraft((prev) => ({ ...prev, [field.id]: e.target.value }))
                  }
                />
              ) : (
                <Input
                  value={draft[field.id] ?? ""}
                  disabled={!interactive}
                  placeholder={field.placeholder}
                  className="w-full text-sm"
                  onChange={(e) =>
                    setDraft((prev) => ({ ...prev, [field.id]: e.target.value }))
                  }
                />
              )}
            </label>
          ))}
          <div className="flex w-full flex-col gap-2 sm:flex-row">
            <Button
              type="button"
              size="sm"
              disabled={!interactive}
              className="w-full min-h-8 touch-manipulation sm:flex-1"
              data-testid="research-clarification-submit"
              onClick={onFormSubmit}
            >
              {pending
                ? t(($) => $.clarification.submitting)
                : t(($) => $.clarification.submit)}
            </Button>
            {question.allow_skip ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={!interactive}
                className="w-full min-h-8 touch-manipulation sm:flex-1"
                data-testid="research-clarification-skip"
                onClick={() => onSkip?.()}
              >
                {t(($) => $.clarification.skip)}
              </Button>
            ) : null}
          </div>
        </div>
      ) : (
        <>
          <div
            className={cn("w-full gap-2", binary ? "grid grid-cols-1 sm:grid-cols-2" : "flex flex-col")}
            data-testid="research-clarification-options"
          >
            {question.options.map((opt) => {
              const selected = selectedOptionId === opt.id;
              return (
                <button
                  key={opt.id}
                  type="button"
                  disabled={!interactive || selected}
                  aria-label={opt.label}
                  aria-pressed={selected}
                  data-testid="research-clarification-option"
                  data-option-id={opt.id}
                  onClick={() => onSelectOption?.(opt.id)}
                  className={cn(
                    "w-full min-h-8 touch-manipulation rounded-md border px-3 py-2 text-left text-sm transition-colors",
                    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    selected
                      ? "border-primary bg-primary/10 text-foreground"
                      : "border-border bg-background hover:bg-accent/60",
                    (!interactive || selected) && !selected && "opacity-60",
                    resolved && !selected && "opacity-50",
                  )}
                >
                  <span className="font-medium">{opt.label}</span>
                  {opt.description ? (
                    <span className="mt-0.5 block text-xs text-muted-foreground">
                      {opt.description}
                    </span>
                  ) : null}
                </button>
              );
            })}
          </div>
          {question.allow_skip && !resolved ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={!interactive}
              className="mt-2 w-full min-h-8 touch-manipulation text-muted-foreground"
              data-testid="research-clarification-skip"
              onClick={() => onSkip?.()}
            >
              {t(($) => $.clarification.skip)}
            </Button>
          ) : null}
        </>
      )}

      {resolution.status === "answered" ? (
        <p
          className="mt-1.5 text-[11px] text-muted-foreground"
          data-testid="research-clarification-answered"
        >
          {resolution.optionLabel
            ? t(($) => $.clarification.answered_option, {
                label: resolution.optionLabel,
              })
            : t(($) => $.clarification.answered)}
        </p>
      ) : null}
      {resolution.status === "skipped" ? (
        <p
          className="mt-1.5 text-[11px] text-muted-foreground"
          data-testid="research-clarification-skipped"
        >
          {t(($) => $.clarification.skipped)}
        </p>
      ) : null}
      {localError ? (
        <p className="mt-1.5 text-xs text-destructive" data-testid="research-clarification-error">
          {localError}
        </p>
      ) : null}
    </div>
  );
}
