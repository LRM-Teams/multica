"use client";

import { Monitor } from "lucide-react";
import type { AgentRuntimeConfig } from "@multica/core/types";
import { HealthDot } from "../../../runtimes/components/shared";
import { useT } from "../../../i18n";

/**
 * Read-only "which computer is this agent bound to" row (Frank, 2026-08-01).
 * Deliberately NOT merged into the Runtime/code-agent picker beside it —
 * that one stays an editable code-agent selector; this is pure info, so
 * relabeling/scoping the picker later can't drag this display along with it.
 *
 * Everything here is served assembled by GET /api/agents/{id}/runtime-config.
 * The name arrives display-ready and connectivity is the daemon's live
 * Workspace Runner socket — this component no longer looks a runtime id up in
 * a list (which silently missed for another member's private runtime) and no
 * longer derives liveness from a runtime's status/heartbeat (Computer-level
 * fact, and runtimes moved to WS presence anyway).
 *
 * The glyph is the machine with connectivity in its corner, the same shape the
 * Computers list uses (Frank, 2026-08-21) — one thing to look at instead of an
 * icon and a loose dot competing for the start of the line. A monitor belongs
 * here rather than on the Runtime row where it used to sit: a Computer is the
 * machine, a runtime is the provider process its daemon core hosts.
 *
 * Connectivity is the dot alone (Frank, 2026-08-21). Spelling out
 * "Connected" pushed the machine name aside to repeat what the colour already
 * said, and grey reads as offline just as plainly as green reads as online.
 * The glyph carries screen-reader-only text either way, so the state is
 * announced rather than left to colour.
 */
export function ComputerInfoRow({
  computer,
}: {
  computer: AgentRuntimeConfig["computer"];
}) {
  const { t } = useT("agents");

  if (!computer) {
    return (
      <span className="text-xs text-muted-foreground">
        {t(($) => $.inspector.computer_none)}
      </span>
    );
  }

  const stateLabel = computer.connected
    ? t(($) => $.inspector.computer_connected)
    : t(($) => $.inspector.computer_disconnected);

  return (
    <span className="inline-flex min-w-0 items-center gap-2 text-[13px]">
      <span className="relative flex size-4 shrink-0 items-center justify-center text-muted-foreground">
        <Monitor className="size-4" aria-hidden />
        <HealthDot
          health={computer.connected ? "online" : "offline"}
          className="absolute -bottom-0.5 -right-0.5 ring-2 ring-background"
        />
        {/* Real text rather than role="img" + aria-label: the state has to
            reach a screen reader without depending on the dot's colour. */}
        <span className="sr-only">{stateLabel}</span>
      </span>
      <span className="min-w-0 truncate font-mono">{computer.name}</span>
      {computer.cli_version && (
        <span className="shrink-0 font-mono text-[11px] text-muted-foreground/70">
          {t(($) => $.inspector.computer_version, { version: computer.cli_version })}
        </span>
      )}
    </span>
  );
}
