"use client";

import { useState } from "react";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export function ResearchDirectorAssignmentPicker({
  agents,
  currentAgentId,
  pending = false,
  error,
  onAssign,
}: {
  agents: readonly Agent[];
  currentAgentId?: string | null;
  pending?: boolean;
  error?: string | null;
  onAssign: (agentId: string, reason: string) => void;
}) {
  const { t } = useT("research");
  const [agentId, setAgentId] = useState(currentAgentId ?? "");
  const [reason, setReason] = useState("");
  const available = agents.filter((agent) => agent.archived_at == null);
  const selected = agentId || currentAgentId || "";

  return (
    <section
      className="border-b border-border/60 bg-card/40 px-3 py-2.5"
      data-testid="research-director-assignment-picker"
    >
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[11px] font-semibold text-foreground">
            {t(($) => $.d5.rail.director_role)}
          </p>
          <p className="truncate text-[10px] text-muted-foreground">
            {t(($) => $.d5.rail.director_fallback)}
          </p>
        </div>
        <span className="rounded-full border border-border px-2 py-0.5 text-[10px] text-muted-foreground">
          {t(($) => $.d5.rail.director_role)}
        </span>
      </div>
      <div className="mt-2 flex gap-2">
        <select
          aria-label={t(($) => $.d5.rail.director_role)}
          value={selected}
          onChange={(event) => setAgentId(event.target.value)}
          className="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1.5 text-xs text-foreground"
          disabled={pending || available.length === 0}
        >
          <option value="">{t(($) => $.d5.rail.director_fallback)}</option>
          {available.map((agent) => (
            <option key={agent.id} value={agent.id}>
              {agent.display_name || agent.name || agent.id}
            </option>
          ))}
        </select>
        <Button
          type="button"
          size="sm"
          disabled={pending || !selected || !reason.trim() || selected === currentAgentId}
          onClick={() => onAssign(selected, reason.trim())}
        >
          {pending ? t(($) => $.d5.rail.director_standby) : t(($) => $.assignments)}
        </Button>
      </div>
      <input
        aria-label={t(($) => $.panel.inspector.reason)}
        value={reason}
        onChange={(event) => setReason(event.target.value)}
        placeholder={t(($) => $.panel.inspector.reason)}
        className={cn(
          "mt-2 w-full rounded-md border border-input bg-background px-2 py-1.5 text-xs text-foreground",
          error && "border-destructive/70",
        )}
        disabled={pending}
      />
      {error ? (
        <p className="mt-1 text-[10px] text-destructive" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}
