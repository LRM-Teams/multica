"use client";

import { Box } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { sandboxNodeTemplatesOptions } from "@multica/core/sandboxes/queries";
import type { SandboxTemplate } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";

export function NodeTemplatesPanel({
  nodeId,
  nodeOnline,
}: {
  nodeId: string;
  nodeOnline: boolean;
}) {
  const { t } = useT("layout");
  const { data, isLoading, error, refetch } = useQuery(sandboxNodeTemplatesOptions(nodeId));
  const templates = data?.templates ?? [];
  const syncedAt = data?.synced_at;
  const online = data?.node_online === true || nodeOnline;

  if (isLoading) {
    return (
      <div className="space-y-3 p-5">
        <Skeleton className="h-4 w-48" />
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-16 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
        <p className="text-sm text-destructive">
          {error instanceof Error
            ? error.message
            : t(($) => $.sandboxes_page.templates_load_failed)}
        </p>
        <Button type="button" size="sm" variant="outline" onClick={() => void refetch()}>
          {t(($) => $.sandboxes_page.templates_retry)}
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4 p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold">{t(($) => $.sandboxes_page.templates_title)}</h3>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.templates_description)}
          </p>
        </div>
        <div className="text-right text-xs text-muted-foreground">
          {!online && (
            <div className="mb-1 text-warning">
              {t(($) => $.sandboxes_page.templates_node_offline)}
            </div>
          )}
          {syncedAt ? (
            <div>
              {t(($) => $.sandboxes_page.templates_synced_at, { time: formatSyncedAt(syncedAt) })}
            </div>
          ) : null}
        </div>
      </div>

      {templates.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed px-6 py-14 text-center">
          <Box className="mb-3 size-8 text-muted-foreground/40" />
          <p className="text-sm font-medium">{t(($) => $.sandboxes_page.templates_empty_title)}</p>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.templates_empty_description)}
          </p>
        </div>
      ) : (
        <div className="divide-y rounded-lg border">
          {templates.map((template) => (
            <TemplateRow key={template.template_id} template={template} />
          ))}
        </div>
      )}
    </div>
  );
}

function TemplateRow({ template }: { template: SandboxTemplate }) {
  const { t } = useT("layout");
  return (
    <div className="flex items-start justify-between gap-4 px-4 py-3">
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate font-mono text-sm font-medium">{template.template_id}</span>
          {template.is_default === true && (
            <Badge variant="secondary">{t(($) => $.sandboxes_page.templates_default_badge)}</Badge>
          )}
          <Badge variant="outline">{template.status || "unknown"}</Badge>
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
          {template.instance_type ? <span>{template.instance_type}</span> : null}
          {template.version ? (
            <span>
              {t(($) => $.sandboxes_page.templates_version, { version: template.version })}
            </span>
          ) : null}
          {template.image_info ? (
            <span className="truncate font-mono">{template.image_info}</span>
          ) : null}
        </div>
        {template.last_error ? (
          <p className="mt-1 text-xs text-destructive">{template.last_error}</p>
        ) : null}
      </div>
    </div>
  );
}

function formatSyncedAt(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
