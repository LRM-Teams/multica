"use client";

import { useEffect, useState } from "react";
import { Check, Copy, Terminal } from "lucide-react";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useComputerReleaseVersion } from "@multica/core/releases/computer-metainfo";
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
}: {
  number: number;
  label: string;
  command: string;
  copyAria: string;
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
          {command}
        </code>
        <CopyButton text={command} ariaLabel={copyAria} />
      </div>
    </div>
  );
}

/**
 * Shared install + workspace-scoped setup command UI.
 *
 * Both onboarding and Connect computer intentionally use this component. It
 * owns release resolution as well as command rendering so the two flows cannot
 * drift between the preferred exact package and the valid `test` fallback.
 */
export function DaemonSetupCommandSteps({
  mode,
  workspaceSlug,
  target,
  installLabel,
  setupLabel,
  setupHint,
  copyAria,
}: {
  mode: DaemonSetupMode;
  workspaceSlug?: string;
  target?: DaemonSetupTarget;
  installLabel: string;
  setupLabel: string;
  setupHint: string;
  copyAria: string;
}) {
  const computerVersion = useComputerReleaseVersion(
    target?.environment ?? "production",
    target?.computerVersion,
  );
  const { installCmd, setupCmd } = daemonSetupCommands(mode, workspaceSlug, {
    ...target,
    computerVersion,
  });

  return (
    <>
      <DaemonSetupCommandStep
        number={1}
        label={installLabel}
        command={installCmd}
        copyAria={copyAria}
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
