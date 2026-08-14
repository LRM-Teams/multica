"use client";

import { useCallback, useState } from "react";
import type { ChannelMessageQuoteInput } from "@multica/core/types";

export interface ComposerQuoteTarget extends ChannelMessageQuoteInput {
  author: string;
  summary: string;
}

export function composerQuotePayloadScope(
  scope: string,
  target: ChannelMessageQuoteInput | null,
): string {
  return [scope, target?.messageId ?? "", target?.selectedText ?? ""].join("\u0000");
}

interface ScopedQuoteTarget {
  scopeKey: string;
  target: ComposerQuoteTarget;
}

/** Owns ephemeral quote state without leaking it across channel/thread switches. */
export function useComposerQuote(scopeKey: string) {
  const [scoped, setScoped] = useState<ScopedQuoteTarget | null>(null);
  const target = scoped?.scopeKey === scopeKey ? scoped.target : null;

  const select = useCallback((next: ComposerQuoteTarget) => {
    setScoped({ scopeKey, target: next });
  }, [scopeKey]);

  const clear = useCallback((expected?: ChannelMessageQuoteInput | null) => {
    setScoped((current) => {
      if (!current || current.scopeKey !== scopeKey) return current;
      if (
        expected &&
        (current.target.messageId !== expected.messageId ||
          current.target.selectedText !== expected.selectedText)
      ) {
        return current;
      }
      return null;
    });
  }, [scopeKey]);
  const cancel = useCallback(() => clear(), [clear]);

  const input: ChannelMessageQuoteInput | undefined = target
    ? { messageId: target.messageId, selectedText: target.selectedText }
    : undefined;

  return { target, input, select, clear, cancel };
}
