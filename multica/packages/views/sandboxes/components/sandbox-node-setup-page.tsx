"use client";

import { useMemo, useState } from "react";
import { Check, Copy, FileCode2, Layers, Loader2, Server } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  sandboxKeys,
  sandboxNodeListOptions,
  sandboxNodeTemplatesOptions,
} from "@multica/core/sandboxes/queries";
import {
  buildSandboxdConfigPath,
  buildSandboxdSetupCommand,
} from "@multica/core/sandboxes/utils";
import type { SandboxNode } from "@multica/core/types";
import { useRequiredWorkspaceSlug, useWorkspacePaths } from "@multica/core/paths";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";

type SetupTab = "setup" | "default_template";

type SetupCommandState =
  | { status: "loading" }
  | { status: "ready"; command: string; configPath: string }
  | { status: "error"; message: string };

const SETUP_LEAF_SKELETON = <Skeleton className="h-4 w-32" />;

function metadataString(metadata: Record<string, unknown> | null | undefined, key: string): string {
  const value = metadata?.[key];
  return typeof value === "string" ? value.trim() : "";
}

function SetupLeafNode({ name, status }: { name: string; status: string }) {
  return (
    <span className="flex min-w-0 items-center gap-2">
      <span className="truncate font-medium">{name}</span>
      <span className="inline-flex shrink-0 items-center gap-1 rounded-md border bg-background px-2 py-0.5 text-xs text-muted-foreground">
        <span
          className={cn(
            "h-1.5 w-1.5 rounded-full",
            status === "online" ? "bg-success" : "bg-muted-foreground/40",
          )}
        />
        {status}
      </span>
    </span>
  );
}

function useSandboxNodeSetupCommand(node: SandboxNode): SetupCommandState {
  const workspaceSlug = useRequiredWorkspaceSlug();
  const user = useAuthStore((s) => s.user);
  const { t } = useT("layout");
  const {
    data,
    isLoading,
    error,
  } = useQuery({
    queryKey: [
      "sandbox-node-setup-command",
      node.id,
      node.node_key,
      node.name,
      workspaceSlug,
      user?.id,
    ],
    queryFn: async () => {
      const token = await api.createSandboxNodeToken(node.id, { name: "sandboxd setup" });
      const serverUrl = api.getBaseUrl?.() || window.location.origin;
      return buildSandboxdSetupCommand({
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
    },
    staleTime: Infinity,
    retry: false,
  });

  if (isLoading) return { status: "loading" };
  if (error || !data) {
    return {
      status: "error",
      message: error instanceof Error ? error.message : t(($) => $.sandboxes_page.view_setup_failed),
    };
  }
  return { status: "ready", command: data.command, configPath: data.configPath };
}

export function SandboxNodeSetupPage({ nodeId }: { nodeId: string }) {
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const { data: nodes = [], isLoading: nodesLoading } = useQuery(sandboxNodeListOptions());
  const node = nodes.find((item) => item.id === nodeId) ?? null;

  if (nodesLoading) {
    return (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <BreadcrumbHeader
          segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
          leaf={SETUP_LEAF_SKELETON}
        />
        <div className="space-y-4 p-6">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-40 w-full max-w-3xl" />
        </div>
      </div>
    );
  }

  if (!node) {
    const notFoundLabel = t(($) => $.sandboxes_page.view_setup_not_found);
    return (
      <div className="flex h-full min-h-0 flex-col bg-background">
        <BreadcrumbHeader
          segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
          leaf={notFoundLabel}
        />
        <div className="flex flex-1 flex-col items-center justify-center text-muted-foreground">
          <Server className="mb-3 size-10 text-muted-foreground/40" />
          <p className="text-sm">{notFoundLabel}</p>
          <Button className="mt-4" variant="outline" onClick={() => navigation.push(paths.sandboxes())}>
            {t(($) => $.sandboxes_page.back_to_list)}
          </Button>
        </div>
      </div>
    );
  }

  return <SandboxNodeSetupContent key={node.id} node={node} />;
}

function SandboxNodeSetupContent({ node }: { node: SandboxNode }) {
  const workspaceSlug = useRequiredWorkspaceSlug();
  const user = useAuthStore((s) => s.user);
  const paths = useWorkspacePaths();
  const { t } = useT("layout");
  const [activeTab, setActiveTab] = useState<SetupTab>("setup");
  const [copied, setCopied] = useState(false);
  const setup = useSandboxNodeSetupCommand(node);
  const nodeStatus = node.status ?? "offline";
  const nodeLeaf = useMemo(
    () => <SetupLeafNode name={node.name} status={nodeStatus} />,
    [node.name, nodeStatus],
  );

  const handleCopy = async () => {
    if (setup.status !== "ready") return;
    if (await copyText(setup.command)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const configPathFallback = buildSandboxdConfigPath({
    workspaceSlug,
    userName: user?.name,
    userEmail: user?.email,
    userId: user?.id,
    serverUrl: api.getBaseUrl?.() || window.location.origin,
  });

  const tabs: { id: SetupTab; icon: typeof FileCode2; label: string }[] = [
    { id: "setup", icon: FileCode2, label: t(($) => $.sandboxes_page.setup_tab) },
    {
      id: "default_template",
      icon: Layers,
      label: t(($) => $.sandboxes_page.default_template_tab),
    },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <BreadcrumbHeader
        segments={[{ href: paths.sandboxes(), label: t(($) => $.sandboxes_page.title) }]}
        leaf={nodeLeaf}
      />

      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex shrink-0 items-center gap-0 overflow-x-auto border-b px-2 md:px-6">
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

        <div className="min-h-0 flex-1 overflow-auto">
          {activeTab === "setup" ? (
            <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 p-6">
              <div>
                <div className="flex items-center gap-2">
                  <FileCode2 className="size-5 text-muted-foreground" />
                  <h1 className="text-xl font-semibold tracking-tight">
                    {t(($) => $.sandboxes_page.setup_page_title)}
                  </h1>
                </div>
                <p className="mt-2 text-sm text-muted-foreground">
                  {t(($) => $.sandboxes_page.setup_page_description, {
                    file: setup.status === "ready" ? setup.configPath : configPathFallback,
                  })}
                </p>
              </div>

              <div className="rounded-lg border bg-card">
                <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium">{node.name}</div>
                    <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{node.node_key}</div>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => void handleCopy()}
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
            <DefaultTemplateTab key={node.id} node={node} />
          )}
        </div>
      </div>
    </div>
  );
}

function DefaultTemplateTab({ node }: { node: SandboxNode }) {
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const {
    data: templatesData,
    isLoading,
    error,
    refetch,
  } = useQuery(sandboxNodeTemplatesOptions(node.id));
  const currentDefault =
    metadataString(node.metadata, "cube_template_id") ||
    templatesData?.default_template_id ||
    "";
  // null = follow server default; string = user draft selection.
  const [draft, setDraft] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const selected = draft ?? currentDefault;

  const templates = templatesData?.templates ?? [];
  const options = [...templates];
  if (currentDefault && !options.some((item) => item.template_id === currentDefault)) {
    options.unshift({
      template_id: currentDefault,
      status: "unknown",
      is_default: true,
    });
  }

  const dirty = draft !== null && draft.trim() !== "" && draft.trim() !== currentDefault;
  const canSave = dirty && !saving;

  const handleSave = async () => {
    const next = selected.trim();
    if (!next) return;
    setSaving(true);
    try {
      await api.updateSandboxNode(node.id, { default_template_id: next });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: sandboxKeys.nodes() }),
        queryClient.invalidateQueries({ queryKey: sandboxKeys.nodeTemplates(node.id) }),
      ]);
      setDraft(null);
      toast.success(t(($) => $.sandboxes_page.default_template_save_success));
    } catch (e) {
      showErrorToast(
        e instanceof Error ? e.message : t(($) => $.sandboxes_page.default_template_save_failed),
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 p-6">
      <div>
        <div className="flex items-center gap-2">
          <Layers className="size-5 text-muted-foreground" />
          <h1 className="text-xl font-semibold tracking-tight">
            {t(($) => $.sandboxes_page.default_template_tab_title)}
          </h1>
        </div>
        <p className="mt-2 text-sm text-muted-foreground">
          {t(($) => $.sandboxes_page.default_template_tab_description)}
        </p>
      </div>

      <div className="space-y-4 rounded-lg border bg-card p-4">
        <div className="space-y-2">
          <Label>{t(($) => $.sandboxes_page.default_template_label)}</Label>
          {isLoading ? (
            <Skeleton className="h-9 w-full" />
          ) : error ? (
            <div className="space-y-3">
              <p className="text-sm text-destructive">
                {error instanceof Error
                  ? error.message
                  : t(($) => $.sandboxes_page.templates_load_failed)}
              </p>
              <Button type="button" size="sm" variant="outline" onClick={() => void refetch()}>
                {t(($) => $.sandboxes_page.templates_retry)}
              </Button>
            </div>
          ) : options.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {t(($) => $.sandboxes_page.default_template_empty_options)}
            </p>
          ) : (
            <Select value={selected} onValueChange={(value) => setDraft(value ?? "")}>
              <SelectTrigger>
                <SelectValue placeholder={t(($) => $.sandboxes_page.default_template_placeholder)} />
              </SelectTrigger>
              <SelectContent>
                {options.map((template) => (
                  <SelectItem key={template.template_id} value={template.template_id}>
                    <span className="font-mono text-xs">{template.template_id}</span>
                    {template.status ? (
                      <span className="ml-2 text-muted-foreground">({template.status})</span>
                    ) : null}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>

        <div className="flex items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground">
            {t(($) => $.sandboxes_page.default_template_sync_hint)}
          </p>
          <Button type="button" size="sm" onClick={() => void handleSave()} disabled={!canSave}>
            {saving ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : null}
            {t(($) => $.sandboxes_page.default_template_save_action)}
          </Button>
        </div>
      </div>
    </div>
  );
}
