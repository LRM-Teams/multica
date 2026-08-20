"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useT } from "../../i18n/use-t";

export function ResearchV6OpsPanel() {
  const wsId = useWorkspaceId();
  const { t } = useT("research");
  const release = useQuery({
    queryKey: ["research", wsId, "v6-release"],
    queryFn: () => api.getResearchV6Release(),
  });
  const monitors = useQuery({
    queryKey: ["research", wsId, "v6-monitors"],
    queryFn: () => api.listResearchMonitors(),
  });
  const windowReport = useQuery({
    queryKey: ["research", wsId, "v6-production-window"],
    queryFn: () => api.getResearchProductionWindow(),
  });
  const createEnabled = release.data?.create_enabled !== false;
  const monitorCount = monitors.data?.monitors.length ?? 0;
  const windowLabel = windowReport.data?.report?.sufficient_data
    ? windowReport.data.report.within_bounds
      ? t(($) => $.v6_ops.window_ok)
      : t(($) => $.v6_ops.window_breach)
    : t(($) => $.v6_ops.window_observing);

  return (
    <aside
      className="relative z-[1] flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-border/70 bg-card/70 px-3 py-2 text-xs text-muted-foreground"
      data-testid="research-v6-ops-panel"
    >
      <span>{createEnabled ? t(($) => $.v6_ops.create_open) : t(($) => $.v6_ops.create_closed)}</span>
      {release.data?.maintenance_reason ? <span>{release.data.maintenance_reason}</span> : null}
      <span>{t(($) => $.v6_ops.monitors, { count: monitorCount })}</span>
      <span>{windowLabel}</span>
    </aside>
  );
}
