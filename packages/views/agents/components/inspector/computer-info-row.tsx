"use client";

import type { AgentRuntimeConfig } from "@multica/core/types";
import { useT } from "../../../i18n";

/**
 * Read-only "which computer is this agent bound to" row (Frank, 2026-08-01).
 * Deliberately NOT merged into the Runtime/code-agent picker row above it —
 * that row stays an editable code-agent selector; this one is pure info,
 * on its own PropRow, so relabeling/scoping the picker later can't drag
 * this display along with it.
 *
 * Everything here is served assembled by GET /api/agents/{id}/runtime-config.
 * The name arrives display-ready and connectivity is the daemon's live
 * Workspace Runner socket — this component no longer looks a runtime id up in
 * a list (which silently missed for another member's private runtime) and no
 * longer derives liveness from a runtime's status/heartbeat (Computer-level
 * fact, and runtimes moved to WS presence anyway).
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

  return (
    <span className="inline-flex min-w-0 items-center gap-1.5 text-xs">
      <span
        className={`shrink-0 h-1.5 w-1.5 rounded-full ${
          computer.connected ? "bg-success" : "bg-muted-foreground/40"
        }`}
      />
      <span className="shrink-0 text-muted-foreground">
        {computer.connected
          ? t(($) => $.inspector.computer_connected)
          : t(($) => $.inspector.computer_disconnected)}
      </span>
      <span className="text-muted-foreground/40">·</span>
      <span className="min-w-0 truncate font-mono">{computer.name}</span>
      {computer.cli_version && (
        <span className="shrink-0 font-mono text-muted-foreground/70">
          {t(($) => $.inspector.computer_version, { version: computer.cli_version })}
        </span>
      )}
    </span>
  );
}
