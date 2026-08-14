"use client";

import { useState } from "react";
import { AlertTriangle, Check, Copy } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";
import {
  formatPrematureGateDiagnostic,
  type PrematureGateDiagnostic,
} from "../lib/research-projection-contract";

export function ResearchProjectionContractNotice({
  diagnostic,
}: {
  diagnostic: PrematureGateDiagnostic;
}) {
  const { t } = useT("research");
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const details = formatPrematureGateDiagnostic(diagnostic);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(details);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  };

  return (
    <aside
      className="relative z-20 flex flex-wrap items-start gap-3 border-b border-warning/30 bg-warning/10 px-4 py-3 text-sm"
      role="status"
      aria-live="polite"
      data-testid="research-projection-contract-notice"
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
      <div className="min-w-0 flex-1">
        <p className="font-medium text-foreground">
          {t(($) => $.d5.projection_contract.title)}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.d5.projection_contract.body, {
            stage: diagnostic.stage,
            codes: diagnostic.findingCodes.join(", ") || "unknown",
          })}
        </p>
        <details className="mt-2 text-xs text-muted-foreground">
          <summary className="cursor-pointer font-medium text-foreground hover:underline">
            {t(($) => $.d5.projection_contract.details)}
          </summary>
          <code
            lang="en"
            dir="ltr"
            className="mt-2 block max-h-32 overflow-auto rounded-md bg-background/70 p-2 whitespace-pre-wrap break-words"
          >
            {details}
          </code>
        </details>
        {copyState === "failed" ? (
          <p className="mt-2 text-xs text-destructive">
            {t(($) => $.d5.projection_contract.copy_failed)}
          </p>
        ) : null}
      </div>
      <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
        {copyState === "copied" ? (
          <Check className="size-3.5" aria-hidden />
        ) : (
          <Copy className="size-3.5" aria-hidden />
        )}
        {t(($) =>
          copyState === "copied"
            ? $.d5.projection_contract.copied
            : $.d5.projection_contract.copy,
        )}
      </Button>
    </aside>
  );
}
