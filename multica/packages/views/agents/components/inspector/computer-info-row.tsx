"use client";

import { deriveRuntimeHealth, runtimeCurrentVersion } from "@multica/core/runtimes";
import type { AgentRuntime } from "@multica/core/types";
import { runtimeComputerLabel } from "../../../runtimes/components/runtime-machines";
import { useT } from "../../../i18n";

/**
 * Read-only "which computer is this agent bound to" row (Frank, 2026-08-01).
 * Deliberately NOT merged into the Runtime/code-agent picker row above it —
 * that row stays an editable code-agent selector; this one is pure info,
 * on its own PropRow, so relabeling/scoping the picker later can't drag
 * this display along with it.
 *
 * Label is machine identity (`runtimeComputerLabel`), never the code-agent
 * `Provider (host)` string. Connection status prefers task #58's
 * `computer_connected` when the server sends it; otherwise falls back to
 * derived runtime health (#10).
 */
export function ComputerInfoRow({ runtime }: { runtime: AgentRuntime | null }) {
  const { t } = useT("agents");

  if (!runtime) {
    return (
      <span className="text-xs text-muted-foreground">
        {t(($) => $.inspector.computer_none)}
      </span>
    );
  }

  const isOnline =
    typeof runtime.computer_connected === "boolean"
      ? runtime.computer_connected
      : deriveRuntimeHealth(runtime, Date.now()) === "online";
  const version = runtimeCurrentVersion(runtime);

  return (
    <span className="inline-flex min-w-0 items-center gap-1.5 text-xs">
      <span
        className={`shrink-0 h-1.5 w-1.5 rounded-full ${
          isOnline ? "bg-success" : "bg-muted-foreground/40"
        }`}
      />
      <span className="shrink-0 text-muted-foreground">
        {isOnline
          ? t(($) => $.inspector.computer_connected)
          : t(($) => $.inspector.computer_disconnected)}
      </span>
      <span className="text-muted-foreground/40">·</span>
      <span className="min-w-0 truncate font-mono">{runtimeComputerLabel(runtime)}</span>
      {version && (
        <span className="shrink-0 font-mono text-muted-foreground/70">
          {t(($) => $.inspector.computer_version, { version })}
        </span>
      )}
    </span>
  );
}
