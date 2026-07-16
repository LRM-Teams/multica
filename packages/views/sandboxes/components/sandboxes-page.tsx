"use client";

import { useEffect, useMemo, useState } from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { Box, FileCode2, Layers, Loader2, Monitor, Plus, RotateCcw, Search, Server, Square, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  useCreateSandboxMutation,
  useDeleteSandboxMutation,
  useResumeSandboxMutation,
  useStopSandboxMutation,
} from "@multica/core/sandboxes/mutations";
import {
  sandboxBindingListOptions,
  sandboxKeys,
  sandboxListOptions,
  sandboxNodeTemplatesOptions,
} from "@multica/core/sandboxes/queries";
import {
  defaultSandboxName,
  effectiveSandboxNodeStatus,
  resolveCreateSandboxTemplate,
  sandboxDisplayName,
} from "@multica/core/sandboxes/utils";
import type { SandboxBinding, SandboxInstance } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@multica/ui/components/ui/resizable";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { toast } from "sonner";
import { PageHeader } from "../../layout/page-header";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";
import { NodeTemplatesPanel } from "./node-templates-panel";

type NodeDetailTab = "sandboxes" | "templates";

type CreateFormState = {
  name: string;
  nodeId: string;
  /** "default" = node configured template; otherwise an explicit Cube template id. */
  templateId: string;
  apiKey: string;
  baseUrl: string;
  model: string;
};

function buildDefaultCreateForm(nodeId: string): CreateFormState {
  return {
    name: defaultSandboxName(),
    nodeId,
    templateId: "default",
    apiKey: "",
    baseUrl: "",
    model: "",
  };
}

function buildRuntimePayload(form: Pick<CreateFormState, "apiKey" | "baseUrl" | "model">) {
  const runtime: Record<string, string> = {};
  const apiKey = form.apiKey.trim();
  const baseUrl = form.baseUrl.trim();
  const model = form.model.trim();
  if (apiKey) runtime.api_key = apiKey;
  if (baseUrl) runtime.base_url = baseUrl;
  if (model) runtime.model = model;
  return runtime;
}

function useNowTick(intervalMs = 10_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

function bindingWithEffectiveStatus(binding: SandboxBinding, now: number): SandboxBinding {
  return {
    ...binding,
    node_status: effectiveSandboxNodeStatus(binding.node_status, binding.node_last_seen_at, now),
  };
}

export function SandboxesPage() {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const isMobile = useIsMobile();
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_sandboxes_layout",
  });

  const [nodeSearch, setNodeSearch] = useState("");
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [createForm, setCreateForm] = useState<CreateFormState>(() => buildDefaultCreateForm(""));
  const [deleteConfirmInstance, setDeleteConfirmInstance] = useState<SandboxInstance | null>(null);
  const [creatingNode, setCreatingNode] = useState(false);

  const now = useNowTick();

  const { data: instances = [], isLoading } = useQuery(sandboxListOptions(wsId));
  const { data: bindings = [], isLoading: bindingsLoading } = useQuery(sandboxBindingListOptions(wsId));

  const connectedBindings = useMemo(() => {
    const result: SandboxBinding[] = [];
    for (const binding of bindings) {
      if (!binding.enabled) continue;
      result.push(bindingWithEffectiveStatus(binding, now));
    }
    return result;
  }, [bindings, now]);
  const hasConnectedNode = connectedBindings.length > 0;

  const instancesByNode = useMemo(() => {
    const map = new Map<string, SandboxInstance[]>();
    for (const instance of instances) {
      const list = map.get(instance.node_id) ?? [];
      list.push(instance);
      map.set(instance.node_id, list);
    }
    return map;
  }, [instances]);

  const filteredBindings = useMemo(() => {
    const query = nodeSearch.trim().toLowerCase();
    if (!query) return connectedBindings;
    return connectedBindings.filter(
      (binding) =>
        binding.node_name.toLowerCase().includes(query) ||
        binding.node_key.toLowerCase().includes(query),
    );
  }, [connectedBindings, nodeSearch]);

  const resolvedNodeId = useMemo(() => {
    if (filteredBindings.length === 0) return null;
    if (
      selectedNodeId !== null &&
      filteredBindings.some((binding) => binding.node_id === selectedNodeId)
    ) {
      return selectedNodeId;
    }
    const preferred =
      filteredBindings.find((binding) => binding.node_status === "online") ?? filteredBindings[0];
    return preferred?.node_id ?? null;
  }, [filteredBindings, selectedNodeId]);

  const selectedBinding =
    connectedBindings.find((binding) => binding.node_id === resolvedNodeId) ?? null;
  const selectedInstances = selectedBinding
    ? (instancesByNode.get(selectedBinding.node_id) ?? [])
    : [];

  const canCreate =
    createForm.name.trim().length > 0 && createForm.nodeId.length > 0;

  const create = useCreateSandboxMutation(wsId);
  const stop = useStopSandboxMutation(wsId);
  const resume = useResumeSandboxMutation(wsId);
  const del = useDeleteSandboxMutation(wsId);

  const createTemplatesQuery = useQuery({
    ...sandboxNodeTemplatesOptions(createForm.nodeId),
    enabled: createDialogOpen && !!createForm.nodeId,
  });
  const createDefaultTemplateId = createTemplatesQuery.data?.default_template_id?.trim() ?? "";
  const createTemplateOptions = useMemo(() => {
    const templates = createTemplatesQuery.data?.templates ?? [];
    if (!createDefaultTemplateId) return templates;
    return templates.filter((item) => item.template_id !== createDefaultTemplateId);
  }, [createTemplatesQuery.data?.templates, createDefaultTemplateId]);

  const openCreateDialog = () => {
    setCreateForm(buildDefaultCreateForm(selectedBinding?.node_id ?? connectedBindings[0]?.node_id ?? ""));
    setCreateDialogOpen(true);
  };

  const handleAddNode = async () => {
    setCreatingNode(true);
    try {
      const suffix = Math.random().toString(36).slice(2, 8);
      const node = await api.createSandboxNode({ name: `sandboxd-${suffix}` });
      await api.bindSandboxNode(wsId, { node_id: node.id });
      await queryClient.invalidateQueries({ queryKey: sandboxKeys.bindings(wsId) });
      await queryClient.invalidateQueries({ queryKey: sandboxKeys.nodes() });
      navigation.push(paths.sandboxNodeSetup(node.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.sandboxes_page.add_node_failed));
    } finally {
      setCreatingNode(false);
    }
  };

  const handleCreateSandbox = async () => {
    if (!canCreate) return;
    try {
      const runtime = buildRuntimePayload(createForm);
      await create.mutateAsync({
        name: createForm.name.trim(),
        node_id: createForm.nodeId,
        template: resolveCreateSandboxTemplate(createForm.templateId),
        ...(Object.keys(runtime).length > 0 ? { runtime } : {}),
      });
      setCreateDialogOpen(false);
      setSelectedNodeId(createForm.nodeId);
      toast.success(t(($) => $.sandboxes_page.create_success));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.sandboxes_page.create_failed));
    }
  };

  if (isLoading || bindingsLoading) {
    return <SandboxesPageSkeleton />;
  }

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Box className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.sandboxes_page.title)}</h1>
          {instances.length > 0 && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
              {instances.length}
            </span>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button type="button" size="sm" variant="outline" onClick={handleAddNode} disabled={creatingNode}>
            {creatingNode ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}
            {t(($) => $.sandboxes_page.add_node_action)}
          </Button>
        </div>
      </PageHeader>

      {!hasConnectedNode ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <EmptyState onAddNode={handleAddNode} addingNode={creatingNode} />
        </div>
      ) : isMobile ? (
        <div className="flex min-h-0 flex-1 flex-col border-t bg-background">
          <NodeSidebar
            bindings={filteredBindings}
            selectedNodeId={resolvedNodeId}
            search={nodeSearch}
            setSearch={setNodeSearch}
            instancesByNode={instancesByNode}
            onSelect={setSelectedNodeId}
          />
          <NodeDetail
            binding={selectedBinding}
            instances={selectedInstances}
            onCreate={openCreateDialog}
            onViewSetup={() => {
              if (selectedBinding) navigation.push(paths.sandboxNodeSetup(selectedBinding.node_id));
            }}
            stoppingId={stop.isPending ? stop.variables : undefined}
            resumingId={resume.isPending ? resume.variables : undefined}
            deletingId={del.isPending ? del.variables : undefined}
            onOpen={(instanceId) => navigation.push(paths.sandboxDetail(instanceId))}
            onStop={(instanceId) => stop.mutate(instanceId)}
            onResume={(instanceId) => resume.mutate(instanceId)}
            onDelete={setDeleteConfirmInstance}
          />
        </div>
      ) : (
        <div className="min-h-0 flex-1 border-t bg-background">
          <ResizablePanelGroup
            orientation="horizontal"
            className="min-h-0 flex-1"
            defaultLayout={defaultLayout}
            onLayoutChanged={onLayoutChanged}
          >
            <ResizablePanel
              id="nodes"
              defaultSize={300}
              minSize={240}
              maxSize={420}
              groupResizeBehavior="preserve-pixel-size"
            >
              <NodeSidebar
                bindings={filteredBindings}
                selectedNodeId={resolvedNodeId}
                search={nodeSearch}
                setSearch={setNodeSearch}
                instancesByNode={instancesByNode}
                onSelect={setSelectedNodeId}
                className="h-full border-b-0 border-r"
              />
            </ResizablePanel>
            <ResizableHandle />
            <ResizablePanel id="detail" minSize="45%">
              <NodeDetail
                binding={selectedBinding}
                instances={selectedInstances}
                onCreate={openCreateDialog}
                onViewSetup={() => {
                  if (selectedBinding) navigation.push(paths.sandboxNodeSetup(selectedBinding.node_id));
                }}
                stoppingId={stop.isPending ? stop.variables : undefined}
                resumingId={resume.isPending ? resume.variables : undefined}
                deletingId={del.isPending ? del.variables : undefined}
                onOpen={(instanceId) => navigation.push(paths.sandboxDetail(instanceId))}
                onStop={(instanceId) => stop.mutate(instanceId)}
                onResume={(instanceId) => resume.mutate(instanceId)}
                onDelete={setDeleteConfirmInstance}
              />
            </ResizablePanel>
          </ResizablePanelGroup>
        </div>
      )}

      <Dialog open={createDialogOpen} onOpenChange={setCreateDialogOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t(($) => $.sandboxes_page.create_dialog_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.sandboxes_page.create_dialog_description)}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="sandbox-name">{t(($) => $.sandboxes_page.name_label)}</Label>
              <Input
                id="sandbox-name"
                value={createForm.name}
                onChange={(e) => setCreateForm((current) => ({ ...current, name: e.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.node_label)}</Label>
              <Select
                value={createForm.nodeId}
                onValueChange={(value) =>
                  setCreateForm((current) => ({
                    ...current,
                    nodeId: value ?? "",
                    templateId: "default",
                  }))
                }
              >
                <SelectTrigger className="h-9 w-full min-w-0">
                  <SelectValue placeholder={t(($) => $.sandboxes_page.select_node_placeholder)} />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                  {connectedBindings.map((binding) => (
                    <SelectItem key={binding.id} value={binding.node_id}>
                      {binding.node_name} ({binding.node_status})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.create_template_label)}</Label>
              {createTemplatesQuery.isLoading ? (
                <Skeleton className="h-9 w-full" />
              ) : (
                <Select
                  value={createForm.templateId}
                  onValueChange={(value) =>
                    setCreateForm((current) => ({ ...current, templateId: value ?? "default" }))
                  }
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue placeholder={t(($) => $.sandboxes_page.create_template_placeholder)} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                    <SelectItem value="default">
                      {createDefaultTemplateId
                        ? t(($) => $.sandboxes_page.create_template_default_option, {
                            id: createDefaultTemplateId,
                          })
                        : t(($) => $.sandboxes_page.create_template_default_option_unset)}
                    </SelectItem>
                    {createTemplateOptions.map((template) => (
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
              {createTemplatesQuery.error ? (
                <p className="text-xs text-destructive">
                  {createTemplatesQuery.error instanceof Error
                    ? createTemplatesQuery.error.message
                    : t(($) => $.sandboxes_page.templates_load_failed)}
                </p>
              ) : null}
            </div>
            <div className="space-y-3">
              <div className="text-sm font-medium">{t(($) => $.sandboxes_page.runtime_model_title)}</div>
              <Input
                type="password"
                placeholder={t(($) => $.sandboxes_page.api_key_placeholder)}
                value={createForm.apiKey}
                onChange={(e) => setCreateForm((current) => ({ ...current, apiKey: e.target.value }))}
              />
              <Input
                placeholder={t(($) => $.sandboxes_page.base_url_placeholder)}
                value={createForm.baseUrl}
                onChange={(e) => setCreateForm((current) => ({ ...current, baseUrl: e.target.value }))}
              />
              <Input
                placeholder={t(($) => $.sandboxes_page.model_placeholder)}
                value={createForm.model}
                onChange={(e) => setCreateForm((current) => ({ ...current, model: e.target.value }))}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateDialogOpen(false)}>
              {t(($) => $.sandboxes_page.cancel_action)}
            </Button>
            <Button onClick={handleCreateSandbox} disabled={!canCreate || create.isPending}>
              {create.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {t(($) => $.sandboxes_page.create_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={!!deleteConfirmInstance}
        onOpenChange={(open) => {
          if (!open) setDeleteConfirmInstance(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.sandboxes_page.delete_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.sandboxes_page.delete_dialog.description, {
                name: deleteConfirmInstance ? sandboxDisplayName(deleteConfirmInstance) : "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.sandboxes_page.delete_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (deleteConfirmInstance) del.mutate(deleteConfirmInstance.id);
                setDeleteConfirmInstance(null);
              }}
            >
              {t(($) => $.sandboxes_page.delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function NodeSidebar({
  bindings,
  selectedNodeId,
  search,
  setSearch,
  instancesByNode,
  onSelect,
  className,
}: {
  bindings: SandboxBinding[];
  selectedNodeId: string | null;
  search: string;
  setSearch: (value: string) => void;
  instancesByNode: Map<string, SandboxInstance[]>;
  onSelect: (nodeId: string) => void;
  className?: string;
}) {
  const { t } = useT("layout");

  return (
    <aside className={cn("flex min-h-0 shrink-0 flex-col border-b bg-muted/20", className)}>
      <div className="shrink-0 border-b bg-background p-3">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={t(($) => $.sandboxes_page.node_search_placeholder)}
            className="h-9 pl-8 text-sm"
          />
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto py-2">
        {bindings.length > 0 ? (
          bindings.map((binding) => (
            <NodeRow
              key={binding.id}
              binding={binding}
              active={binding.node_id === selectedNodeId}
              instanceCount={instancesByNode.get(binding.node_id)?.length ?? 0}
              onClick={() => onSelect(binding.node_id)}
            />
          ))
        ) : (
          <div className="flex h-full flex-col items-center justify-center px-6 text-center">
            <Search className="h-8 w-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">{t(($) => $.sandboxes_page.no_node_matches_title)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.sandboxes_page.no_node_matches_hint)}</p>
          </div>
        )}
      </div>
    </aside>
  );
}

function NodeRow({
  binding,
  active,
  instanceCount,
  onClick,
}: {
  binding: SandboxBinding;
  active: boolean;
  instanceCount: number;
  onClick: () => void;
}) {
  const { t } = useT("layout");
  const online = binding.node_status === "online";

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "group flex w-full min-w-0 items-start gap-3 px-4 py-2.5 text-left transition-colors",
        active ? "bg-accent" : "hover:bg-accent/50",
      )}
    >
      <span className="relative mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border bg-background">
        <Monitor className="h-3.5 w-3.5 text-muted-foreground" />
        <span
          className={cn(
            "absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full ring-2 ring-background",
            online ? "bg-success" : "bg-muted-foreground/40",
          )}
        />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{binding.node_name}</span>
        <span className="mt-1 block truncate font-mono text-xs text-muted-foreground">{binding.node_key}</span>
        <span className="mt-1.5 block text-xs text-muted-foreground">
          {t(($) => $.sandboxes_page.instance_count, { count: instanceCount })}
        </span>
      </span>
    </button>
  );
}

function NodeDetail({
  binding,
  instances,
  onCreate,
  onViewSetup,
  stoppingId,
  resumingId,
  deletingId,
  onOpen,
  onStop,
  onResume,
  onDelete,
}: {
  binding: SandboxBinding | null;
  instances: SandboxInstance[];
  onCreate: () => void;
  onViewSetup: () => void;
  stoppingId?: string;
  resumingId?: string;
  deletingId?: string;
  onOpen: (instanceId: string) => void;
  onStop: (instanceId: string) => void;
  onResume: (instanceId: string) => void;
  onDelete: (instance: SandboxInstance) => void;
}) {
  const { t } = useT("layout");
  const [activeTab, setActiveTab] = useState<NodeDetailTab>("sandboxes");

  useEffect(() => {
    setActiveTab("sandboxes");
  }, [binding?.node_id]);

  if (!binding) {
    return (
      <main className="flex min-h-0 flex-1 flex-col items-center justify-center px-6 text-center">
        <Server className="h-8 w-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm text-muted-foreground">{t(($) => $.sandboxes_page.select_node_hint)}</p>
      </main>
    );
  }

  const online = binding.node_status === "online";
  const runningCount = instances.filter((instance) => instance.status === "running").length;
  const tabs: { id: NodeDetailTab; icon: typeof Box; label: string }[] = [
    { id: "sandboxes", icon: Box, label: t(($) => $.sandboxes_page.sandboxes_tab) },
    { id: "templates", icon: Layers, label: t(($) => $.sandboxes_page.templates_tab) },
  ];

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
      <div className="shrink-0 border-b bg-background px-5 py-4">
        <div className="flex min-w-0 flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <h2 className="truncate text-xl font-semibold tracking-tight">{binding.node_name}</h2>
              <span className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-0.5 text-xs text-muted-foreground">
                <span
                  className={cn(
                    "h-1.5 w-1.5 rounded-full",
                    online ? "bg-success" : "bg-muted-foreground/40",
                  )}
                />
                {binding.node_status}
              </span>
            </div>
            <div className="mt-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span className="font-mono">{binding.node_key}</span>
              <span className="text-muted-foreground/40">·</span>
              <span>{t(($) => $.sandboxes_page.instance_count, { count: instances.length })}</span>
              {runningCount > 0 && (
                <>
                  <span className="text-muted-foreground/40">·</span>
                  <span className="text-primary">
                    {t(($) => $.sandboxes_page.running_count, { count: runningCount })}
                  </span>
                </>
              )}
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button type="button" size="sm" variant="outline" onClick={onViewSetup}>
              <FileCode2 className="h-3 w-3" />
              {t(($) => $.sandboxes_page.view_setup_action)}
            </Button>
            {activeTab === "sandboxes" && (
              <Button type="button" size="sm" onClick={onCreate}>
                <Plus className="h-3 w-3" />
                {t(($) => $.sandboxes_page.create_action)}
              </Button>
            )}
          </div>
        </div>
      </div>

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

      <div className="min-h-0 flex-1 overflow-auto">
        {activeTab === "templates" ? (
          <NodeTemplatesPanel nodeId={binding.node_id} nodeOnline={online} />
        ) : instances.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center px-6 py-16 text-center">
            <Box className="mb-3 size-8 text-muted-foreground/50" />
            <div className="font-medium">{t(($) => $.sandboxes_page.empty_title)}</div>
            <p className="mt-1 max-w-sm text-sm text-muted-foreground">
              {t(($) => $.sandboxes_page.empty_node_description)}
            </p>
            <Button type="button" size="sm" className="mt-4" onClick={onCreate}>
              <Plus className="h-3 w-3" />
              {t(($) => $.sandboxes_page.create_action)}
            </Button>
          </div>
        ) : (
          <div className="divide-y">
            {instances.map((instance) => (
              <SandboxRow
                key={instance.id}
                instance={instance}
                stopping={stoppingId === instance.id}
                resuming={resumingId === instance.id}
                deleting={deletingId === instance.id}
                onOpen={() => onOpen(instance.id)}
                onStop={() => onStop(instance.id)}
                onResume={() => onResume(instance.id)}
                onDelete={() => onDelete(instance)}
              />
            ))}
          </div>
        )}
      </div>
    </main>
  );
}

function SandboxRow({
  instance,
  stopping,
  resuming,
  deleting,
  onOpen,
  onStop,
  onResume,
  onDelete,
}: {
  instance: SandboxInstance;
  stopping: boolean;
  resuming: boolean;
  deleting: boolean;
  onOpen: () => void;
  onStop: () => void;
  onResume: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("layout");
  const canStop = instance.status === "running";
  const canResume = instance.status === "stopped";
  const canDelete = instance.status !== "reconfiguring" && instance.status !== "resuming";
  const displayName = sandboxDisplayName(instance);

  return (
    <div className="flex items-center justify-between gap-4 px-5 py-3.5">
      <button type="button" className="min-w-0 flex-1 text-left" onClick={onOpen}>
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{displayName}</span>
          <StatusBadge status={instance.status} />
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          {instance.local_ref ?? t(($) => $.sandboxes_page.waiting_local)}
        </div>
        {instance.error && <div className="mt-1 text-xs text-destructive">{instance.error}</div>}
      </button>
      <div className="flex shrink-0 items-center gap-2">
        {canResume ? (
          <Button size="sm" variant="outline" disabled={resuming} onClick={onResume}>
            {resuming ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <RotateCcw className="mr-2 size-3.5" />}
            {t(($) => $.sandboxes_page.resume_action)}
          </Button>
        ) : (
          <Button size="sm" variant="outline" disabled={!canStop || stopping} onClick={onStop}>
            {stopping ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <Square className="mr-2 size-3.5" />}
            {t(($) => $.sandboxes_page.stop_action)}
          </Button>
        )}
        <Button size="sm" variant="ghost" disabled={!canDelete || deleting} onClick={onDelete}>
          <Trash2 className="mr-2 size-3.5" /> {t(($) => $.sandboxes_page.delete_action)}
        </Button>
      </div>
    </div>
  );
}

function EmptyState({
  onAddNode,
  addingNode,
}: {
  onAddNode: () => void;
  addingNode: boolean;
}) {
  const { t } = useT("layout");
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted">
        <Server className="h-6 w-6 text-muted-foreground" />
      </div>
      <h2 className="mt-4 text-base font-semibold">{t(($) => $.sandboxes_page.no_bound_node_title)}</h2>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">{t(($) => $.sandboxes_page.no_bound_node)}</p>
      <Button type="button" size="sm" className="mt-5" onClick={onAddNode} disabled={addingNode}>
        {addingNode ? <Loader2 className="mr-2 size-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}
        {t(($) => $.sandboxes_page.add_node_action)}
      </Button>
    </div>
  );
}

function SandboxesPageSkeleton() {
  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="justify-between px-5">
        <Skeleton className="h-4 w-24" />
      </PageHeader>
      <div className="flex min-h-0 flex-1 border-t">
        <div className="hidden w-[300px] shrink-0 border-r p-3 md:block">
          <Skeleton className="h-9 w-full rounded-md" />
          <div className="mt-5 space-y-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-20 w-full rounded-lg" />
            ))}
          </div>
        </div>
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="border-b p-5">
            <Skeleton className="h-6 w-64 rounded-md" />
            <Skeleton className="mt-3 h-4 w-full max-w-md rounded-md" />
          </div>
          <div className="space-y-2 p-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-14 w-full rounded-md" />
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const variant =
    status === "online" || status === "running"
      ? "default"
      : status === "failed"
        ? "destructive"
        : status === "reconfiguring" || status === "creating" || status === "resuming" || status === "stopping"
          ? "secondary"
          : "secondary";
  return <Badge variant={variant}>{status}</Badge>;
}
