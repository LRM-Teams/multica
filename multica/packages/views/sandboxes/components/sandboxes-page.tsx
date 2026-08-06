"use client";

import { useEffect, useMemo, useReducer, useState } from "react";
import { useDefaultLayout } from "react-resizable-panels";
import {
  Box,
  Camera,
  FileCode2,
  Layers,
  Loader2,
  Monitor,
  Plus,
  RotateCcw,
  Search,
  Server,
  Square,
  Trash2,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  useCreateSandboxMutation,
  useCreateSandboxTemplateMutation,
  useDeleteSandboxMutation,
  useResumeSandboxMutation,
  useStopSandboxMutation,
} from "@multica/core/sandboxes/mutations";
import {
  sandboxBindingListOptions,
  sandboxKeys,
  sandboxListOptions,
  sandboxNodeDockerImagesOptions,
  sandboxNodeTemplatesOptions,
} from "@multica/core/sandboxes/queries";
import {
  buildSandboxRuntimePayload,
  defaultSandboxName,
  defaultSandboxSnapshotName,
  emptySandboxRuntimeForm,
  resolveCreateSandboxTemplate,
  sandboxDisplayName,
  type SandboxRuntimeFormState,
} from "@multica/core/sandboxes/utils";
import type { DockerImage, SandboxBinding, SandboxInstance, SandboxSnapshot } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
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
import { getCurrentWsId } from "@multica/core/platform";
import { api } from "@multica/core/api";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { PageHeader } from "../../layout/page-header";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";
import { SandboxRuntimeForm } from "./sandbox-runtime-form";
import { NodeTemplatesPanel } from "./node-templates-panel";
import { NodeSnapshotsPanel } from "./node-snapshots-panel";

type NodeDetailTab = "sandboxes" | "docker" | "templates" | "snapshots";

const EMPTY_SANDBOX_INSTANCES: SandboxInstance[] = [];
const EMPTY_SANDBOX_BINDINGS: SandboxBinding[] = [];

type DockerContainerCreateRequest = {
  name: string;
  nodeId: string;
  image: string;
  runtime: SandboxRuntimeFormState;
};

type CreateFormState = {
  name: string;
  nodeId: string;
  /** "default" = node configured template; otherwise an explicit Cube template id. */
  templateId: string;
  /** When true, template (and node) are fixed — e.g. create from a snapshot. */
  templateLocked: boolean;
  lockedTemplateLabel: string;
  runtime: SandboxRuntimeFormState;
};

function buildDefaultCreateForm(nodeId: string): CreateFormState {
  return {
    name: defaultSandboxName(),
    nodeId,
    templateId: "default",
    templateLocked: false,
    lockedTemplateLabel: "",
    runtime: emptySandboxRuntimeForm(),
  };
}

function buildCreateFormFromSnapshot(snapshot: SandboxSnapshot): CreateFormState {
  return {
    name: defaultSandboxName(),
    nodeId: snapshot.node_id,
    templateId: snapshot.cube_snapshot_id.trim(),
    templateLocked: true,
    lockedTemplateLabel: snapshot.name.trim() || snapshot.cube_snapshot_id,
    runtime: emptySandboxRuntimeForm(),
  };
}

type PageUiState = {
  nodeSearch: string;
  selectedNodeId: string | null;
  createDialogOpen: boolean;
  dockerCreateOpen: boolean;
  createForm: CreateFormState;
  deleteConfirmInstance: SandboxInstance | null;
  snapshotInstance: SandboxInstance | null;
  snapshotName: string;
  snapshotDescription: string;
  creatingNode: boolean;
};

type PageUiAction =
  | { type: "set_node_search"; value: string }
  | { type: "set_selected_node"; value: string | null }
  | { type: "open_create"; form: CreateFormState }
  | { type: "set_create_open"; open: boolean }
  | { type: "set_docker_create_open"; open: boolean }
  | { type: "patch_create_form"; patch: Partial<CreateFormState> }
  | { type: "set_delete_confirm"; instance: SandboxInstance | null }
  | { type: "open_snapshot"; instance: SandboxInstance }
  | { type: "close_snapshot" }
  | { type: "set_snapshot_name"; value: string }
  | { type: "set_snapshot_description"; value: string }
  | { type: "set_creating_node"; value: boolean };

function pageUiReducer(state: PageUiState, action: PageUiAction): PageUiState {
  switch (action.type) {
    case "set_node_search":
      return { ...state, nodeSearch: action.value };
    case "set_selected_node":
      return { ...state, selectedNodeId: action.value };
    case "open_create":
      return { ...state, createDialogOpen: true, createForm: action.form };
    case "set_create_open":
      return { ...state, createDialogOpen: action.open };
    case "set_docker_create_open":
      return { ...state, dockerCreateOpen: action.open };
    case "patch_create_form":
      return { ...state, createForm: { ...state.createForm, ...action.patch } };
    case "set_delete_confirm":
      return { ...state, deleteConfirmInstance: action.instance };
    case "open_snapshot":
      return {
        ...state,
        snapshotInstance: action.instance,
        snapshotName: defaultSandboxSnapshotName(action.instance),
        snapshotDescription: "",
      };
    case "close_snapshot":
      return { ...state, snapshotInstance: null };
    case "set_snapshot_name":
      return { ...state, snapshotName: action.value };
    case "set_snapshot_description":
      return { ...state, snapshotDescription: action.value };
    case "set_creating_node":
      return { ...state, creatingNode: action.value };
    default:
      return state;
  }
}

const initialPageUiState: PageUiState = {
  nodeSearch: "",
  selectedNodeId: null,
  createDialogOpen: false,
  dockerCreateOpen: false,
  createForm: buildDefaultCreateForm(""),
  deleteConfirmInstance: null,
  snapshotInstance: null,
  snapshotName: "",
  snapshotDescription: "",
  creatingNode: false,
};

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

  const [ui, dispatch] = useReducer(pageUiReducer, initialPageUiState);
  const {
    nodeSearch,
    selectedNodeId,
    createDialogOpen,
    dockerCreateOpen,
    createForm,
    deleteConfirmInstance,
    snapshotInstance,
    snapshotName,
    snapshotDescription,
    creatingNode,
  } = ui;

  // Polling remounts/reflows the page and can dismiss open overlays (Dialog
  // unmount → onOpenChange(false), or NodeDetail key change wiping local UI).
  const overlayOpen =
    createDialogOpen ||
    dockerCreateOpen ||
    !!snapshotInstance ||
    !!deleteConfirmInstance;

  const { data: instances, isPending: instancesPending } = useQuery({
    ...sandboxListOptions(wsId),
    refetchInterval: overlayOpen ? false : 2000,
  });
  const { data: bindings, isPending: bindingsPending } = useQuery({
    ...sandboxBindingListOptions(wsId),
    refetchInterval: overlayOpen ? false : 5000,
  });
  // Stable empty fallbacks — `?? []` allocates a new array every render and
  // would invalidate the useMemos below while data is still undefined.
  const instanceList = instances ?? EMPTY_SANDBOX_INSTANCES;
  const bindingList = bindings ?? EMPTY_SANDBOX_BINDINGS;
  const showInitialLoading =
    (instancesPending && instances === undefined) || (bindingsPending && bindings === undefined);

  // Trust the API's already-computed node_status. Re-deriving from cached
  // last_seen_at with a local clock tick caused false offline flaps between
  // refetches even while sandboxd kept heartbeating.
  const connectedBindings = useMemo(
    () => bindingList.filter((binding) => binding.enabled),
    [bindingList],
  );
  const hasConnectedNode = connectedBindings.length > 0;

  const instancesByNode = useMemo(() => {
    const map = new Map<string, SandboxInstance[]>();
    for (const instance of instanceList) {
      const list = map.get(instance.node_id) ?? [];
      list.push(instance);
      map.set(instance.node_id, list);
    }
    return map;
  }, [instanceList]);

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

  // Pin the resolved node into UI state so a later bindings poll cannot flip
  // "preferred online" and remount NodeDetail (which destroys local dialogs).
  useEffect(() => {
    if (!resolvedNodeId) return;
    if (selectedNodeId === resolvedNodeId) return;
    if (
      selectedNodeId !== null &&
      filteredBindings.some((binding) => binding.node_id === selectedNodeId)
    ) {
      return;
    }
    dispatch({ type: "set_selected_node", value: resolvedNodeId });
  }, [filteredBindings, resolvedNodeId, selectedNodeId]);

  const selectedBinding =
    connectedBindings.find((binding) => binding.node_id === resolvedNodeId) ?? null;
  const selectedInstances = selectedBinding
    ? (instancesByNode.get(selectedBinding.node_id) ?? [])
    : [];

  const canCreate =
    createForm.name.trim().length > 0 &&
    createForm.nodeId.length > 0 &&
    (!createForm.templateLocked || createForm.templateId.length > 0);

  const create = useCreateSandboxMutation(wsId);
  const stop = useStopSandboxMutation(wsId);
  const resume = useResumeSandboxMutation(wsId);
  const del = useDeleteSandboxMutation(wsId);
  const createTemplate = useCreateSandboxTemplateMutation(wsId);

  const openSnapshotDialog = (instance: SandboxInstance) => {
    dispatch({ type: "open_snapshot", instance });
  };

  const handleCreateTemplate = async () => {
    if (!snapshotInstance) return;
    const name = snapshotName.trim();
    if (!name) return;
    try {
      await createTemplate.mutateAsync({
        instanceId: snapshotInstance.id,
        name,
        description: snapshotDescription.trim(),
      });
      dispatch({ type: "close_snapshot" });
      toast.success(t(($) => $.sandboxes_page.create_template_success));
    } catch (e) {
      showErrorToast(
        e instanceof Error ? e.message : t(($) => $.sandboxes_page.create_template_failed),
      );
    }
  };

  const {
    data: createTemplatesData,
    isLoading: createTemplatesLoading,
    error: createTemplatesError,
  } = useQuery({
    ...sandboxNodeTemplatesOptions(createForm.nodeId),
    enabled: createDialogOpen && !!createForm.nodeId,
    // Keep the create dialog's Select stable; template list polls when closed.
    refetchInterval: createDialogOpen ? false : 10_000,
  });
  const createDefaultTemplateId = createTemplatesData?.default_template_id?.trim() ?? "";
  const createTemplateOptions = useMemo(() => {
    const templates = createTemplatesData?.templates ?? [];
    if (!createDefaultTemplateId) return templates;
    return templates.filter((item) => item.template_id !== createDefaultTemplateId);
  }, [createTemplatesData?.templates, createDefaultTemplateId]);

  const openCreateDialog = () => {
    dispatch({
      type: "open_create",
      form: buildDefaultCreateForm(selectedBinding?.node_id ?? connectedBindings[0]?.node_id ?? ""),
    });
  };

  const openCreateFromSnapshot = (snapshot: SandboxSnapshot) => {
    dispatch({ type: "open_create", form: buildCreateFormFromSnapshot(snapshot) });
  };

  const handleAddNode = async () => {
    dispatch({ type: "set_creating_node", value: true });
    try {
      // Prefer the platform singleton so a mid-flight workspace switch cannot
      // bind the new node to a stale wsId from the render closure.
      const workspaceId = getCurrentWsId() || wsId;
      const suffix = Math.random().toString(36).slice(2, 8);
      const node = await api.createSandboxNode({ name: `sandboxd-${suffix}` });
      await api.bindSandboxNode(workspaceId, { node_id: node.id });
      // Invalidate in the background; navigation does not need to wait.
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: sandboxKeys.bindings(workspaceId) }),
        queryClient.invalidateQueries({ queryKey: sandboxKeys.nodes() }),
      ]);
      navigation.push(paths.sandboxNodeSetup(node.id));
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      if (/insufficient permissions/i.test(message)) {
        showErrorToast(t(($) => $.sandboxes_page.add_node_permission_denied));
      } else {
        showErrorToast(message || t(($) => $.sandboxes_page.add_node_failed));
      }
    } finally {
      dispatch({ type: "set_creating_node", value: false });
    }
  };

  const handleCreateSandbox = async () => {
    if (!canCreate) return;
    try {
      const runtime = buildSandboxRuntimePayload(createForm.runtime);
      await create.mutateAsync({
        name: createForm.name.trim(),
        node_id: createForm.nodeId,
        template: resolveCreateSandboxTemplate(createForm.templateId),
        ...(runtime ? { runtime } : {}),
      });
      dispatch({ type: "set_create_open", open: false });
      dispatch({ type: "set_selected_node", value: createForm.nodeId });
      toast.success(t(($) => $.sandboxes_page.create_success));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.sandboxes_page.create_failed));
    }
  };

  const handleCreateDockerContainer = async ({
    name,
    nodeId,
    image,
    runtime,
  }: DockerContainerCreateRequest) => {
    try {
      const runtimePayload = buildSandboxRuntimePayload(runtime);
      await create.mutateAsync({
        name: name.trim(),
        node_id: nodeId,
        docker_image: image,
        ...(runtimePayload ? { runtime: runtimePayload } : {}),
      });
      dispatch({ type: "set_selected_node", value: nodeId });
      toast.success(t(($) => $.sandboxes_page.docker_create_success));
    } catch (e) {
      showErrorToast(
        e instanceof Error ? e.message : t(($) => $.sandboxes_page.docker_create_failed),
      );
      throw e;
    }
  };

  if (showInitialLoading) {
    return <SandboxesPageSkeleton />;
  }

  const nodeDetailProps = {
    binding: selectedBinding,
    instances: selectedInstances,
    onCreate: openCreateDialog,
    onCreateDockerContainer: handleCreateDockerContainer,
    creatingDockerContainer: create.isPending,
    dockerCreateOpen,
    onDockerCreateOpenChange: (open: boolean) =>
      dispatch({ type: "set_docker_create_open", open }),
    onCreateFromSnapshot: openCreateFromSnapshot,
    onViewSetup: () => {
      if (selectedBinding) navigation.push(paths.sandboxNodeSetup(selectedBinding.node_id));
    },
    stoppingId: stop.isPending ? stop.variables : undefined,
    resumingId: resume.isPending ? resume.variables : undefined,
    deletingId: del.isPending ? del.variables : undefined,
    creatingTemplateId: createTemplate.isPending ? createTemplate.variables?.instanceId : undefined,
    onOpen: (instanceId: string) => navigation.push(paths.sandboxDetail(instanceId)),
    onStop: (instanceId: string) => stop.mutate(instanceId),
    onResume: (instanceId: string) => resume.mutate(instanceId),
    onCreateTemplate: openSnapshotDialog,
    onDelete: (instance: SandboxInstance) => dispatch({ type: "set_delete_confirm", instance }),
  };

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Box className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.sandboxes_page.title)}</h1>
          {instanceList.length > 0 && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
              {instanceList.length}
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
            setSearch={(value) => dispatch({ type: "set_node_search", value })}
            instancesByNode={instancesByNode}
            onSelect={(value) => dispatch({ type: "set_selected_node", value })}
          />
          <NodeDetail {...nodeDetailProps} />
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
                setSearch={(value) => dispatch({ type: "set_node_search", value })}
                instancesByNode={instancesByNode}
                onSelect={(value) => dispatch({ type: "set_selected_node", value })}
                className="h-full border-b-0 border-r"
              />
            </ResizablePanel>
            <ResizableHandle />
            <ResizablePanel id="detail" minSize="45%">
              <NodeDetail {...nodeDetailProps} />
            </ResizablePanel>
          </ResizablePanelGroup>
        </div>
      )}

      <Dialog
        open={createDialogOpen}
        onOpenChange={(open) => dispatch({ type: "set_create_open", open })}
      >
        <DialogContent className="sm:max-w-xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {createForm.templateLocked
                ? t(($) => $.sandboxes_page.create_from_snapshot_dialog_title)
                : t(($) => $.sandboxes_page.create_dialog_title)}
            </DialogTitle>
            <DialogDescription>
              {createForm.templateLocked
                ? t(($) => $.sandboxes_page.create_from_snapshot_dialog_description)
                : t(($) => $.sandboxes_page.create_dialog_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="sandbox-name">{t(($) => $.sandboxes_page.name_label)}</Label>
              <Input
                id="sandbox-name"
                value={createForm.name}
                onChange={(e) =>
                  dispatch({ type: "patch_create_form", patch: { name: e.target.value } })
                }
              />
            </div>
            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.node_label)}</Label>
              {createForm.templateLocked ? (
                <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
                  {connectedBindings.find((b) => b.node_id === createForm.nodeId)?.node_name ??
                    createForm.nodeId}
                </div>
              ) : (
                <Select
                  value={createForm.nodeId}
                  onValueChange={(value) =>
                    dispatch({
                      type: "patch_create_form",
                      patch: { nodeId: value ?? "", templateId: "default" },
                    })
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
              )}
            </div>
            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.create_template_label)}</Label>
              {createForm.templateLocked ? (
                <div className="space-y-1">
                  <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
                    <div className="font-medium">{createForm.lockedTemplateLabel}</div>
                    <div className="mt-0.5 break-all font-mono text-xs text-muted-foreground">
                      {createForm.templateId}
                    </div>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.sandboxes_page.create_from_snapshot_template_hint)}
                  </p>
                </div>
              ) : createTemplatesLoading ? (
                <Skeleton className="h-9 w-full" />
              ) : (
                <Select
                  value={createForm.templateId}
                  onValueChange={(value) =>
                    dispatch({
                      type: "patch_create_form",
                      patch: { templateId: value ?? "default" },
                    })
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
              {!createForm.templateLocked && createTemplatesError ? (
                <p className="text-xs text-destructive">
                  {createTemplatesError instanceof Error
                    ? createTemplatesError.message
                    : t(($) => $.sandboxes_page.templates_load_failed)}
                </p>
              ) : null}
            </div>
            <SandboxRuntimeForm
              value={createForm.runtime}
              onChange={(runtime) =>
                dispatch({ type: "patch_create_form", patch: { runtime } })
              }
            />
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => dispatch({ type: "set_create_open", open: false })}
            >
              {t(($) => $.sandboxes_page.cancel_action)}
            </Button>
            <Button onClick={() => void handleCreateSandbox()} disabled={!canCreate || create.isPending}>
              {create.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {t(($) => $.sandboxes_page.create_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!snapshotInstance}
        onOpenChange={(open) => {
          if (!open) dispatch({ type: "close_snapshot" });
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t(($) => $.sandboxes_page.snapshot_dialog_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.sandboxes_page.snapshot_dialog_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="snapshot-name">{t(($) => $.sandboxes_page.snapshot_name_label)}</Label>
              <Input
                id="snapshot-name"
                value={snapshotName}
                onChange={(e) => dispatch({ type: "set_snapshot_name", value: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="snapshot-description">
                {t(($) => $.sandboxes_page.snapshot_description_label)}
              </Label>
              <Textarea
                id="snapshot-description"
                value={snapshotDescription}
                onChange={(e) =>
                  dispatch({ type: "set_snapshot_description", value: e.target.value })
                }
                placeholder={t(($) => $.sandboxes_page.snapshot_description_placeholder)}
                rows={3}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => dispatch({ type: "close_snapshot" })}>
              {t(($) => $.sandboxes_page.cancel_action)}
            </Button>
            <Button
              onClick={() => void handleCreateTemplate()}
              disabled={!snapshotName.trim() || createTemplate.isPending}
            >
              {createTemplate.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {t(($) => $.sandboxes_page.create_template_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={!!deleteConfirmInstance}
        onOpenChange={(open) => {
          if (!open) dispatch({ type: "set_delete_confirm", instance: null });
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
                dispatch({ type: "set_delete_confirm", instance: null });
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

function NodeDetail(props: {
  binding: SandboxBinding | null;
  instances: SandboxInstance[];
  onCreate: () => void;
  onCreateDockerContainer: (request: DockerContainerCreateRequest) => Promise<void>;
  creatingDockerContainer: boolean;
  dockerCreateOpen: boolean;
  onDockerCreateOpenChange: (open: boolean) => void;
  onCreateFromSnapshot: (snapshot: SandboxSnapshot) => void;
  onViewSetup: () => void;
  stoppingId?: string;
  resumingId?: string;
  deletingId?: string;
  creatingTemplateId?: string;
  onOpen: (instanceId: string) => void;
  onStop: (instanceId: string) => void;
  onResume: (instanceId: string) => void;
  onCreateTemplate: (instance: SandboxInstance) => void;
  onDelete: (instance: SandboxInstance) => void;
}) {
  const { t } = useT("layout");
  if (!props.binding) {
    return (
      <main className="flex min-h-0 flex-1 flex-col items-center justify-center px-6 text-center">
        <Server className="h-8 w-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm text-muted-foreground">{t(($) => $.sandboxes_page.select_node_hint)}</p>
      </main>
    );
  }

  return <NodeDetailContent key={props.binding.node_id} {...props} binding={props.binding} />;
}

function isDockerContainerInstance(instance: SandboxInstance): boolean {
  const endpointKind = typeof instance.endpoint_info.kind === "string" ? instance.endpoint_info.kind : "";
  const creationMode =
    typeof instance.metadata.creation_mode === "string" ? instance.metadata.creation_mode : "";
  return endpointKind === "docker" || creationMode === "docker_container" || instance.template.startsWith("docker:");
}

function dockerContainerImage(instance: SandboxInstance): string {
  if (typeof instance.metadata.docker_image === "string" && instance.metadata.docker_image.trim()) {
    return instance.metadata.docker_image.trim();
  }
  return instance.template.startsWith("docker:") ? instance.template.slice("docker:".length) : "";
}

function DockerContainerRow({
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
  const canDelete =
    instance.status !== "reconfiguring" &&
    instance.status !== "resuming" &&
    instance.status !== "snapshotting";
  const image = dockerContainerImage(instance);

  return (
    <div className="flex items-center justify-between gap-4 px-5 py-3.5">
      <button type="button" className="min-w-0 flex-1 text-left" onClick={onOpen}>
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{sandboxDisplayName(instance)}</span>
          <StatusBadge status={instance.status} />
        </div>
        <div className="mt-1 truncate font-mono text-xs text-muted-foreground">
          {image || instance.local_ref || t(($) => $.sandboxes_page.waiting_local)}
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

function NodeDetailContent({
  binding,
  instances,
  onCreate,
  onCreateDockerContainer,
  creatingDockerContainer,
  dockerCreateOpen,
  onDockerCreateOpenChange,
  onCreateFromSnapshot,
  onViewSetup,
  stoppingId,
  resumingId,
  deletingId,
  creatingTemplateId,
  onOpen,
  onStop,
  onResume,
  onCreateTemplate,
  onDelete,
}: {
  binding: SandboxBinding;
  instances: SandboxInstance[];
  onCreate: () => void;
  onCreateDockerContainer: (request: DockerContainerCreateRequest) => Promise<void>;
  creatingDockerContainer: boolean;
  dockerCreateOpen: boolean;
  onDockerCreateOpenChange: (open: boolean) => void;
  onCreateFromSnapshot: (snapshot: SandboxSnapshot) => void;
  onViewSetup: () => void;
  stoppingId?: string;
  resumingId?: string;
  deletingId?: string;
  creatingTemplateId?: string;
  onOpen: (instanceId: string) => void;
  onStop: (instanceId: string) => void;
  onResume: (instanceId: string) => void;
  onCreateTemplate: (instance: SandboxInstance) => void;
  onDelete: (instance: SandboxInstance) => void;
}) {
  const { t } = useT("layout");
  const [activeTab, setActiveTab] = useState<NodeDetailTab>("sandboxes");

  const online = binding.node_status === "online";
  const dockerInstances = instances.filter(isDockerContainerInstance);
  const sandboxInstances = instances.filter((instance) => !isDockerContainerInstance(instance));
  const runningCount = instances.filter((instance) => instance.status === "running").length;
  const tabs: { id: NodeDetailTab; icon: typeof Box; label: string }[] = [
    { id: "sandboxes", icon: Box, label: t(($) => $.sandboxes_page.sandboxes_tab) },
    { id: "docker", icon: Server, label: t(($) => $.sandboxes_page.docker_tab) },
    { id: "templates", icon: Layers, label: t(($) => $.sandboxes_page.templates_tab) },
    { id: "snapshots", icon: Camera, label: t(($) => $.sandboxes_page.snapshots_tab) },
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
        {activeTab === "docker" ? (
          <DockerContainersPanel
            nodeId={binding.node_id}
            nodeOnline={online}
            instances={dockerInstances}
            creating={creatingDockerContainer}
            dialogOpen={dockerCreateOpen}
            onDialogOpenChange={onDockerCreateOpenChange}
            stoppingId={stoppingId}
            resumingId={resumingId}
            deletingId={deletingId}
            onCreate={onCreateDockerContainer}
            onOpen={onOpen}
            onStop={onStop}
            onResume={onResume}
            onDelete={onDelete}
          />
        ) : activeTab === "templates" ? (
          <NodeTemplatesPanel nodeId={binding.node_id} nodeOnline={online} />
        ) : activeTab === "snapshots" ? (
          <NodeSnapshotsPanel
            nodeId={binding.node_id}
            onCreateSandbox={onCreateFromSnapshot}
          />
        ) : sandboxInstances.length === 0 ? (
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
            {sandboxInstances.map((instance) => (
              <SandboxRow
                key={instance.id}
                instance={instance}
                stopping={stoppingId === instance.id}
                resuming={resumingId === instance.id}
                deleting={deletingId === instance.id}
                creatingTemplate={creatingTemplateId === instance.id}
                onOpen={() => onOpen(instance.id)}
                onStop={() => onStop(instance.id)}
                onResume={() => onResume(instance.id)}
                onCreateTemplate={() => onCreateTemplate(instance)}
                onDelete={() => onDelete(instance)}
              />
            ))}
          </div>
        )}
      </div>
    </main>
  );
}

function dockerImageLabel(image: DockerImage): string {
  return image.image_ref || [image.repository, image.tag].filter(Boolean).join(":");
}

function defaultDockerContainerName(): string {
  return `docker-${Math.random().toString(36).slice(2, 8)}`;
}

function DockerContainersPanel({
  nodeId,
  nodeOnline,
  instances,
  creating,
  dialogOpen,
  onDialogOpenChange,
  stoppingId,
  resumingId,
  deletingId,
  onCreate,
  onOpen,
  onStop,
  onResume,
  onDelete,
}: {
  nodeId: string;
  nodeOnline: boolean;
  instances: SandboxInstance[];
  creating: boolean;
  dialogOpen: boolean;
  onDialogOpenChange: (open: boolean) => void;
  stoppingId?: string;
  resumingId?: string;
  deletingId?: string;
  onCreate: (request: DockerContainerCreateRequest) => Promise<void>;
  onOpen: (instanceId: string) => void;
  onStop: (instanceId: string) => void;
  onResume: (instanceId: string) => void;
  onDelete: (instance: SandboxInstance) => void;
}) {
  const { t } = useT("layout");
  const [name, setName] = useState(defaultDockerContainerName);
  const [selectedImageRef, setSelectedImageRef] = useState("");
  const [runtime, setRuntime] = useState(emptySandboxRuntimeForm);
  const { data, isLoading, error, refetch } = useQuery({
    ...sandboxNodeDockerImagesOptions(nodeId),
    enabled: dialogOpen && !!nodeId,
    refetchInterval: dialogOpen ? false : 10_000,
  });

  const images = useMemo(
    () => (data?.images ?? []).filter((image) => dockerImageLabel(image).trim().length > 0),
    [data?.images],
  );
  const selectedImage =
    images.find((image) => dockerImageLabel(image) === selectedImageRef) ?? images[0] ?? null;
  const image = selectedImage ? dockerImageLabel(selectedImage) : "";
  const dockerImagesError = data?.error?.trim() ?? "";
  const canCreate = nodeOnline && !creating && !!selectedImage && name.trim().length > 0;

  const openDialog = () => {
    setName(defaultDockerContainerName());
    setSelectedImageRef("");
    setRuntime(emptySandboxRuntimeForm());
    onDialogOpenChange(true);
  };

  const submit = async () => {
    if (!canCreate || !selectedImage) return;
    try {
      await onCreate({ name, nodeId, image, runtime });
      onDialogOpenChange(false);
    } catch {
      // The caller owns toast/error presentation; keep the dialog open for retry.
    }
  };

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex shrink-0 items-start justify-between gap-3 border-b px-5 py-4">
        <div className="min-w-0">
          <h3 className="text-base font-semibold">{t(($) => $.sandboxes_page.docker_title)}</h3>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.docker_description)}
          </p>
        </div>
        <Button type="button" size="sm" onClick={openDialog} disabled={!nodeOnline}>
          <Plus className="h-3 w-3" />
          {t(($) => $.sandboxes_page.docker_create_action)}
        </Button>
      </div>

      {!nodeOnline ? (
        <div className="mx-5 mt-4 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
          {t(($) => $.sandboxes_page.docker_node_offline_hint)}
        </div>
      ) : null}

      {instances.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center px-6 py-16 text-center">
          <Server className="mb-3 size-8 text-muted-foreground/50" />
          <div className="font-medium">{t(($) => $.sandboxes_page.docker_empty_title)}</div>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.docker_empty_description)}
          </p>
          <Button type="button" size="sm" className="mt-4" onClick={openDialog} disabled={!nodeOnline}>
            <Plus className="h-3 w-3" />
            {t(($) => $.sandboxes_page.docker_create_action)}
          </Button>
        </div>
      ) : (
        <div className="divide-y">
          {instances.map((instance) => (
            <DockerContainerRow
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

      <Dialog open={dialogOpen} onOpenChange={onDialogOpenChange}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{t(($) => $.sandboxes_page.docker_create_dialog_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.sandboxes_page.docker_create_dialog_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="docker-container-name">
                {t(($) => $.sandboxes_page.docker_name_label)}
              </Label>
              <Input
                id="docker-container-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t(($) => $.sandboxes_page.docker_name_placeholder)}
              />
            </div>

            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.docker_image_label)}</Label>
              {isLoading ? (
                <Skeleton className="h-9 w-full" />
              ) : error || dockerImagesError ? (
                <div className="flex flex-col items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-3">
                  <div className="text-sm font-medium text-destructive">
                    {t(($) => $.sandboxes_page.docker_images_load_failed)}
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {error instanceof Error
                      ? error.message
                      : dockerImagesError || t(($) => $.sandboxes_page.templates_load_failed)}
                  </p>
                  <Button type="button" variant="outline" size="sm" onClick={() => void refetch()}>
                    {t(($) => $.sandboxes_page.templates_retry)}
                  </Button>
                </div>
              ) : images.length === 0 ? (
                <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
                  {t(($) => $.sandboxes_page.docker_images_empty_description)}
                </div>
              ) : (
                <Select
                  value={selectedImage ? dockerImageLabel(selectedImage) : ""}
                  onValueChange={(value) => {
                    if (typeof value === "string") setSelectedImageRef(value);
                  }}
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue placeholder={t(($) => $.sandboxes_page.docker_image_placeholder)} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                    {images.map((item) => {
                      const ref = dockerImageLabel(item);
                      return (
                        <SelectItem key={ref} value={ref}>
                          <span className="truncate">{ref}</span>
                          {item.size ? <span className="ml-2 text-muted-foreground">{item.size}</span> : null}
                        </SelectItem>
                      );
                    })}
                  </SelectContent>
                </Select>
              )}
              <p className="text-xs text-muted-foreground">
                {t(($) => $.sandboxes_page.docker_image_hint)}
              </p>
            </div>

            <SandboxRuntimeForm value={runtime} onChange={setRuntime} />

          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => onDialogOpenChange(false)}>
              {t(($) => $.sandboxes_page.cancel_action)}
            </Button>
            <Button onClick={() => void submit()} disabled={!canCreate}>
              {creating ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {t(($) => $.sandboxes_page.docker_create_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function SandboxRow({
  instance,
  stopping,
  resuming,
  deleting,
  creatingTemplate,
  onOpen,
  onStop,
  onResume,
  onCreateTemplate,
  onDelete,
}: {
  instance: SandboxInstance;
  stopping: boolean;
  resuming: boolean;
  deleting: boolean;
  creatingTemplate: boolean;
  onOpen: () => void;
  onStop: () => void;
  onResume: () => void;
  onCreateTemplate: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("layout");
  const canStop = instance.status === "running";
  const canResume = instance.status === "stopped";
  const canCreateTemplate = instance.status === "running" && !!instance.local_ref;
  const canDelete =
    instance.status !== "reconfiguring" &&
    instance.status !== "resuming" &&
    instance.status !== "snapshotting";
  const displayName = sandboxDisplayName(instance);
  const snapshotBusy = creatingTemplate || instance.status === "snapshotting";

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
          <Button size="sm" variant="outline" disabled={!canStop || stopping || snapshotBusy} onClick={onStop}>
            {stopping ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <Square className="mr-2 size-3.5" />}
            {t(($) => $.sandboxes_page.stop_action)}
          </Button>
        )}
        <Button
          size="sm"
          variant="outline"
          disabled={!canCreateTemplate || snapshotBusy}
          onClick={onCreateTemplate}
        >
          {snapshotBusy ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <Camera className="mr-2 size-3.5" />}
          {t(($) => $.sandboxes_page.create_template_action)}
        </Button>
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
        : status === "reconfiguring" ||
            status === "creating" ||
            status === "resuming" ||
            status === "stopping" ||
            status === "snapshotting"
          ? "secondary"
          : "secondary";
  return <Badge variant={variant}>{status}</Badge>;
}
