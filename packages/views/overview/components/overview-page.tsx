"use client";

import { LayoutDashboard } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { KpiCards } from "./kpi-cards";
import { MyTasksPanel } from "./my-tasks-panel";
import { AgentStatusPanel } from "./agent-status-panel";

/**
 * Workspace admin overview ("总览"). Real data: active-agent breakdown, task /
 * approval counts (issue status totals), and the two inbox-driven panels (tasks
 * for me + agent activity). Mock placeholders: the spend card and the
 * "vs yesterday" trend tags (see mock.ts). Reproduced from the Figma design,
 * embedded in the existing dashboard shell (the design's own sidebar dropped).
 */
export function OverviewPage() {
  const { t } = useT("overview");
  const wsId = useWorkspaceId();

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="gap-2">
        <LayoutDashboard className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
      </PageHeader>

      <div className="flex min-h-0 flex-1 flex-col gap-4 p-4 md:p-6">
        <KpiCards wsId={wsId} />
        <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-2">
          <MyTasksPanel wsId={wsId} />
          <AgentStatusPanel wsId={wsId} />
        </div>
      </div>
    </div>
  );
}
