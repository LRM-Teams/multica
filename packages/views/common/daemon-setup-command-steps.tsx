"use client";

import { useEffect, useState } from "react";
import { Check, Copy, RefreshCw, Terminal } from "lucide-react";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import {
  type DaemonSetupTarget,
  type DaemonSetupMode,
  daemonSetupCommands,
} from "./daemon-setup-commands";

function CopyButton({ text, ariaLabel }: { text: string; ariaLabel: string }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timeout = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(timeout);
  }, [copied]);

  const handleCopy = () => {
    void copyText(text).then((ok) => {
      if (ok) setCopied(true);
    });
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      aria-label={ariaLabel}
      className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-success" aria-hidden />
      ) : (
        <Copy className="h-3.5 w-3.5" aria-hidden />
      )}
    </button>
  );
}

function DaemonSetupCommandStep({
  number,
  label,
  command,
  copyAria,
  state,
  errorLabel,
  retryLabel,
  onRetry,
}: {
  number: number;
  label: string;
  command: string;
  copyAria: string;
  state?: "loading" | "error";
  errorLabel?: string;
  retryLabel?: string;
  onRetry?: () => void;
}) {
  return (
    <div>
      <p className="mb-1.5 text-xs font-medium text-foreground">
        {number}. {label}
      </p>
      <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2.5 font-mono text-sm">
        <Terminal
          className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground"
          aria-hidden
        />
        <code
          className={cn(
            "min-w-0 flex-1 break-all whitespace-pre-wrap tabular-nums",
            CODE_LIGATURE_CLASS,
          )}
        >
          {state === "loading" ? (
            <span
              className="my-0.5 block h-4 w-48 animate-pulse rounded bg-muted-foreground/15"
              aria-hidden
            />
          ) : state === "error" ? (
            errorLabel
          ) : (
            command
          )}
        </code>
        {state ? (
          onRetry && (
            <button
              type="button"
              onClick={onRetry}
              className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              aria-label={retryLabel}
            >
              <RefreshCw className="h-3.5 w-3.5" aria-hidden />
            </button>
          )
        ) : (
          <CopyButton text={command} ariaLabel={copyAria} />
        )}
      </div>
    </div>
  );
}

/**
 * Shared install + workspace-scoped setup command UI.
 *
 * Both onboarding and Connect computer intentionally use this component so a
 * CLI command or copy interaction change cannot drift between the two flows.
 */
export function DaemonSetupCommandSteps({
  mode,
  workspaceSlug,
  target,
  installLabel,
  setupLabel,
  setupHint,
  copyAria,
  installState,
  installErrorLabel,
  installRetryLabel,
  onInstallRetry,
}: {
  mode: DaemonSetupMode;
  workspaceSlug?: string;
  target?: DaemonSetupTarget;
  installLabel: string;
  setupLabel: string;
  setupHint: string;
  copyAria: string;
  installState?: "loading" | "error";
  installErrorLabel?: string;
  installRetryLabel?: string;
  onInstallRetry?: () => void;
}) {
  const { installCmd, setupCmd } = daemonSetupCommands(mode, workspaceSlug, target);

  return (
    <>
      <DaemonSetupCommandStep
        number={1}
        label={installLabel}
        command={installCmd}
        copyAria={copyAria}
        state={installState}
        errorLabel={installErrorLabel}
        retryLabel={installRetryLabel}
        onRetry={onInstallRetry}
      />
      <div>
        <DaemonSetupCommandStep
          number={2}
          label={setupLabel}
          command={setupCmd}
          copyAria={copyAria}
        />
        <p className="mt-1.5 text-[11px] leading-[1.55] text-muted-foreground">
          {setupHint}
        </p>
      </div>
    </>
  );
}
