"use client";

import { useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n/use-t";

export function NoteHighlightsCompose({
  initialText,
  submitting = false,
  onSend,
  onCancel,
}: {
  initialText: string;
  submitting?: boolean;
  onSend: (text: string) => void;
  onCancel?: () => void;
}) {
  const { t } = useT("layout");
  const [text, setText] = useState(initialText);
  const canSend = text.trim().length > 0 && !submitting;

  return (
    <div className="mx-3 mb-2 space-y-3 rounded-xl border bg-card px-3 py-3" data-testid="highlights-compose">
      <p className="text-[11px] leading-4 text-muted-foreground">
        {t(($) => $.notes_page.assistant_highlights_compose_hint)}
      </p>
      <Textarea
        value={text}
        onChange={(event) => setText(event.target.value)}
        disabled={submitting}
        aria-label={t(($) => $.notes_page.assistant_satellite_highlights)}
        data-testid="highlights-compose-text"
        className="min-h-32 max-h-56 resize-y overflow-y-auto field-sizing-fixed"
      />
      <div className="flex justify-end gap-2">
        {onCancel ? (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={submitting}
            data-testid="highlights-cancel"
            onClick={onCancel}
          >
            {t(($) => $.notes_page.assistant_highlights_cancel)}
          </Button>
        ) : null}
        <Button
          type="button"
          size="sm"
          disabled={!canSend}
          data-testid="highlights-send"
          onClick={() => {
            const next = text.trim();
            if (!next) return;
            onSend(next);
          }}
        >
          {t(($) => $.notes_page.assistant_highlights_send)}
        </Button>
      </div>
    </div>
  );
}
