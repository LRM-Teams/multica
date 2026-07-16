"use client";

import { useEffect, useState } from "react";
import { Box, Check, Copy, FileCode2, Layers, Loader2, Server } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  sandboxNodeListOptions,
  sandboxNodeTemplatesOptions,
} from "@multica/core/sandboxes/queries";
import {
  buildSandboxdConfigPath,
  buildSandboxdSetupCommand,
  effectiveSandboxNodeStatus,
} from "@multica/core/sandboxes/utils";
import type { SandboxNode, SandboxTemplate } from "@multica/core/types";
import { useRequiredWorkspaceSlug, useWorkspacePaths } from "@multica/core/paths";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";

type SetupTab = "setup" | "templates";

type SetupCommandState =
  | { status: "loading" }
  | { status: "ready"; command: string; configPath: string }
  | { status: "error"; message: string };

function useNowTick(intervalMs = 10_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

function useSandboxNodeSetupCommand(node: SandboxNode | null): SetupCommandState {
  const workspaceSlug = useRequiredWorkspaceSlug();
  const user = useAuthStore((s) => s.user);
  const { t } = useT("layout");
  const [state, setState] = useState<SetupCommandState>({ status: "loading" });

  useEffect(() => {
    if (!node) {
      setState({ status: "loading" });
      return;
    }

    let cancelled = false;
    setState({ status: "loading" });

    void (async () => {
      try {
        const token = await api.createSandboxNodeToken(node.id, { name: "sandboxd setup" });
        if (cancelled) return;
        const serverUrl = api.getBaseUrl?.() || window.location.origin;
        const { command, configPath } = buildSandboxdSetupCommand({
          serverUrl,
          nodeToken: token.token,
          nodeKey: node.node_key,
          name: node.name,
          ownerUserId: node.owner_user_id || node.node_key,
          workspaceSlug,
          userName: user?.name,
          userEmail: user?.email,
          userId: user?.id,
          metadata: node.metadata,
        });
        setState({ status: "ready", command, configPath });
      } catch (e) {
        if (cancelled) return;
        setState({
          status: "error",
          message: e instanceof Error ? e.message : t(($) => $.sandboxes_page.view_setup_failed),
        });
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [node, t, user?.email, user?.id, user?.name, workspaceSlug]);

  return state;
}

function metadataString(metadata: Record<string, unknown> | null | undefined, key: string): string {
  const value = metadata?.[key];
  return typeof value === "string" ? value.trim() : "";
}

export function SandboxNodeSetupPage({ nodeId }: { nodeId: string }) {
  const workspaceSlug = useRequiredWorkspaceSlug();
  const user = useAuthStore((s) => s.user);
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const now = useNowTick();
  const [activeTab, setActiveTab] = useState<SetupTab>("setup");
  const [copied, setCopied] = useState(false);

  const { data: nodes = [], isLoading: nodesLoading } = useQuery(sandboxNodeListOptions());
  const node = nodes.find((item) => item.id === nodeId) ?? null;
  const setup = useSandboxNodeSetupCommand(node);
  const templatesQuery = useQuery({
    ...sandboxNodeTemplatesOptions(nodeId),
    enabled: !!node,
  });

  const nodeStatus = node
    ? effectiveSandboxNodeStatus(node.status, node.last_seen_at, now)
    : "offline";
  const defaultTemplateId = node
    ? metadataString(node.metadata, "cube_template_id")
    : "";

  const handleCopy = async () => {
    if (setup.status !== "ready") return;
    if (await copyText(setup.command)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  useEffect(() => {
    if (setup.status === "error") {
      toast.error(setup.message);
    }
  }, [setup]);

  if (nodesLoading) {
    return (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <BreadcrumbHeader
          segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
          leaf={<Skeleton className="h-4 w-32" />}
        />
        <div className="space-y-4 p-6">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-40 w-full" />
        </div>
      </div>
    );
  }

  if (!node) {
    return (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <BreadcrumbHeader
          segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
          leaf={
            <span className="truncate text-muted-foreground">
              {t(($) => $.sandboxes_page.view_setup_not_found)}
            </span>
          }
        />
        <div className="flex flex-1 flex-col items-center justify-center text-muted-foreground">
          <Server className="mb-3 size-10 text-muted-foreground/40" />
          <p className="text-sm">{t(($) => $.sandboxes_page.view_setup_not_found)}</p>
          <Button className="mt-4" variant="outline" onClick={() => navigation.push(paths.sandboxes())}>
            {t(($) => $.sandboxes_page.back_to_list)}
          </Button>
        </div>
      </div>
    );
  }

  const configPathFallback = buildSandboxdConfigPath({
    workspaceSlug,
    userName: user?.name,
    userEmail: user?.email,
    userId: user?.id,
    serverUrl: api.getBaseUrl?.() || window.location.origin,
  });

  const tabs: { id: SetupTab; icon: typeof FileCode2; label: string }[] = [
    {
      id: "setup",
      icon: FileCode2,
      label: t(($) => $.sandboxes_page.setup_tab),
    },
    {
      id: "templates",
      icon: Layers,
      label: t(($) => $.sandboxes_page.templates_tab),
    },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <BreadcrumbHeader
        segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
        leaf={
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium">{node.name}</span>
            <span className="inline-flex shrink-0 items-center gap-1 rounded-md border bg-background px-2 py-0.5 text-xs text-muted-foreground">
              <span
                className={cn(
                  "h-1.5 w-1.5 rounded-full",
                  nodeStatus === "online" ? "bg-success" : "bg-muted-foreground/40",
                )}
              />
              {nodeStatus}
            </span>
          </span>
        }
      />

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3 md:grid md:grid-cols-[320px_minmax(0,1fr)] md:gap-4 md:overflow-hidden md:p-6">
        <aside className="shrink-0 space-y-3 rounded-lg border bg-background p-4">
          <div className="flex items-start gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-muted/40">
              <Server className="size-5 text-muted-foreground" />
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">{node.name}</div>
              <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{node.node_key}</div>
            </div>
          </div>
          <dl className="space-y-2 text-xs">
            <div className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">{t(($) => $.sandboxes_page.node_status_label)}</dt>
              <dd className="inline-flex items-center gap-1.5 font-medium">
                <span
                  className={cn(
                    "h-1.5 w-1.5 rounded-full",
                    nodeStatus === "online" ? "bg-success" : "bg-muted-foreground/40",
                  )}
                />
                {nodeStatus}
              </dd>
            </div>
            <div className="flex items-start justify-between gap-3">
              <dt className="shrink-0 text-muted-foreground">
                {t(($) => $.sandboxes_page.default_template_label)}
              </dt>
              <dd className="min-w-0 truncate text-right font-mono">
                {defaultTemplateId || t(($) => $.sandboxes_page.default_template_unset)}
              </dd>
            </div>
          </dl>
        </aside>

        <div className="flex min-h-[60vh] flex-col overflow-hidden rounded-lg border bg-background md:h-full md:min-h-0">
          <div className="flex shrink-0 items-center gap-0 overflow-x-auto border-b px-2 md:px-4">
            {tabs.map((tab) => (
              <button
                key={tab.id}
                type="button"
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  "flex shrink-0 items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2.5 text-xs font-medium transition-colors",
                  activeTab === tab.id
                    ? "border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <tab.icon className="h-3.5 w-3.5" />
                {tab.label}
              </button>
            ))}
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto">
            {activeTab === "setup" ? (
              <div className="space-y-4 p-4 md:p-5">
                <div>
                  <h2 className="text-sm font-semibold">{t(($) => $.sandboxes_page.setup_page_title)}</h2>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {t(($) => $.sandboxes_page.setup_page_description, {
                      file: setup.status === "ready" ? setup.configPath : configPathFallback,
                    })}
                  </p>
                </div>

                <div className="rounded-lg border bg-card">
                  <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
                    <div className="min-w-0 text-sm font-medium">
                      {t(($) => $.sandboxes_page.setup_command_label)}
                    </div>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={handleCopy}
                      disabled={setup.status !== "ready"}
                    >
                      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                      {copied
                        ? t(($) => $.sandboxes_page.copied_action)
                        : t(($) => $.sandboxes_page.copy_command_action)}
                    </Button>
                  </div>
                  <div className="p-4">
                    {setup.status === "loading" ? (
                      <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t(($) => $.sandboxes_page.setup_page_loading)}
                      </div>
                    ) : setup.status === "error" ? (
                      <p className="text-sm text-destructive">{setup.message}</p>
                    ) : (
                      <code
                        className={cn(
                          "block max-h-[min(28rem,60vh)] overflow-auto whitespace-pre-wrap break-all font-mono text-xs leading-relaxed select-all",
                          CODE_LIGATURE_CLASS,
                        )}
                      >
                        {setup.command}
                      </code>
                    )}
                  </div>
                </div>
              </div>
            ) : (
              <TemplatesTab
                loading={templatesQuery.isLoading}
                error={templatesQuery.error}
                templates={templatesQuery.data?.templates ?? []}
                syncedAt={templatesQuery.data?.synced_at}
                nodeOnline={templatesQuery.data?.node_online === true || nodeStatus === "online"}
                onRetry={() => void templatesQuery.refetch()}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function TemplatesTab({
  loading,
  error,
  templates,
  syncedAt,
  nodeOnline,
  onRetry,
}: {
  loading: boolean;
  error: unknown;
  templates: SandboxTemplate[];
  syncedAt?: string;
  nodeOnline: boolean;
  onRetry: () => void;
}) {
  const { t } = useT("layout");

  if (loading) {
    return (
      <div className="space-y-3 p-4 md:p-5">
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
          {error instanceof Error ? error.message : t(($) => $.sandboxes_page.templates_load_failed)}
        </p>
        <Button type="button" size="sm" variant="outline" onClick={onRetry}>
          {t(($) => $.sandboxes_page.templates_retry)}
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4 p-4 md:p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold">{t(($) => $.sandboxes_page.templates_title)}</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.templates_description)}
          </p>
        </div>
        <div className="text-right text-xs text-muted-foreground">
          {!nodeOnline && (
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
          {template.version ? <span>v{template.version}</span> : null}
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
