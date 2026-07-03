"use client";

import { useEffect, useMemo, useReducer, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, Check, Copy, Loader2, Plus, RotateCcw, Square, Trash2 } from "lucide-react";
import {
  useCreateSandboxMutation,
  useDeleteSandboxMutation,
  useResumeSandboxMutation,
  useStopSandboxMutation,
} from "@multica/core/sandboxes/mutations";
import { sandboxBindingListOptions, sandboxKeys, sandboxListOptions } from "@multica/core/sandboxes/queries";
import { defaultSandboxName, sandboxDisplayName } from "@multica/core/sandboxes/utils";
import type { SandboxBinding, SandboxInstance } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { copyText } from "@multica/ui/lib/clipboard";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { toast } from "sonner";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";

type AddNodeState = {
  selectedNodeId: string;
  dialogOpen: boolean;
  setupCommand: string;
  setupCopied: boolean;
  creating: boolean;
};

type AddNodeAction =
  | { type: "selectNode"; nodeId: string }
  | { type: "startCreating" }
  | { type: "setupReady"; command: string }
  | { type: "createFinished" }
  | { type: "setDialogOpen"; open: boolean }
  | { type: "copySuccess" }
  | { type: "copyReset" };

const initialAddNodeState: AddNodeState = {
  selectedNodeId: "",
  dialogOpen: false,
  setupCommand: "",
  setupCopied: false,
  creating: false,
};

function addNodeReducer(state: AddNodeState, action: AddNodeAction): AddNodeState {
  switch (action.type) {
    case "selectNode":
      return { ...state, selectedNodeId: action.nodeId };
    case "startCreating":
      return { ...state, creating: true, setupCopied: false };
    case "setupReady":
      return { ...state, creating: false, setupCommand: action.command, dialogOpen: true };
    case "createFinished":
      return { ...state, creating: false };
    case "setDialogOpen":
      return { ...state, dialogOpen: action.open };
    case "copySuccess":
      return { ...state, setupCopied: true };
    case "copyReset":
      return { ...state, setupCopied: false };
    default:
      return state;
  }
}

type CreateFormState = {
  name: string;
  nodeId: string;
  apiKey: string;
  baseUrl: string;
  model: string;
};

function buildDefaultCreateForm(bindings: SandboxBinding[]): CreateFormState {
  const connected = bindings.filter((binding) => binding.enabled);
  const preferred =
    connected.find((binding) => binding.node_status === "online") ?? connected[0];
  return {
    name: defaultSandboxName(),
    nodeId: preferred?.node_id ?? "",
    apiKey: "",
    baseUrl: "",
    model: "",
  };
}

export function SandboxesPage() {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [createForm, setCreateForm] = useState<CreateFormState>(() => buildDefaultCreateForm([]));
  const [addNode, dispatchAddNode] = useReducer(addNodeReducer, initialAddNodeState);
  const { dialogOpen: addDialogOpen, setupCommand, setupCopied, creating: creatingNode } = addNode;
  const { data: instances = [], isLoading } = useQuery(sandboxListOptions(wsId));
  const { data: bindings = [] } = useQuery(sandboxBindingListOptions(wsId));

  const connectedBindings = useMemo(() => bindings.filter((binding) => binding.enabled), [bindings]);
  const hasConnectedNode = connectedBindings.length > 0;
  const canCreate =
    hasConnectedNode &&
    createForm.name.trim().length > 0 &&
    createForm.nodeId.length > 0 &&
    createForm.apiKey.trim().length > 0;

  const create = useCreateSandboxMutation(wsId);
  const stop = useStopSandboxMutation(wsId);
  const resume = useResumeSandboxMutation(wsId);
  const del = useDeleteSandboxMutation(wsId);

  useEffect(() => {
    if (!createDialogOpen) return;
    setCreateForm(buildDefaultCreateForm(bindings));
  }, [createDialogOpen, bindings]);

  const handleAddNode = async () => {
    dispatchAddNode({ type: "startCreating" });
    try {
      const suffix = Math.random().toString(36).slice(2, 8);
      const node = await api.createSandboxNode({ name: `sandboxd-${suffix}` });
      const token = await api.createSandboxNodeToken(node.id, { name: "sandboxd setup" });
      await api.bindSandboxNode(wsId, { node_id: node.id });
      await queryClient.invalidateQueries({ queryKey: sandboxKeys.bindings(wsId) });
      const config = {
        server_url: api.getBaseUrl?.() || window.location.origin,
        node_token: token.token,
        node_key: node.node_key,
        name: node.name,
        owner_user_id: node.owner_user_id || node.node_key,
        sandbox_server: "http://127.0.0.1:8000",
        cube_proxy_http: "http://127.0.0.1",
        cube_domain: "cube.app",
        cube_template_id: "YOUR_CUBE_TEMPLATE_ID",
        concurrency: 1,
        poll_interval: "5s",
      };
      dispatchAddNode({
        type: "setupReady",
        command: `mkdir -p .multica && cat > .multica/sandboxd.json <<'EOF'
${JSON.stringify(config, null, 2)}
EOF
multica sandboxd`,
      });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.sandboxes_page.add_node_failed));
      dispatchAddNode({ type: "createFinished" });
    }
  };

  const handleCopySetup = async () => {
    if (await copyText(setupCommand)) {
      dispatchAddNode({ type: "copySuccess" });
      setTimeout(() => dispatchAddNode({ type: "copyReset" }), 2000);
    }
  };

  const handleCreateSandbox = async () => {
    if (!canCreate) return;
    try {
      await create.mutateAsync({
        name: createForm.name.trim(),
        node_id: createForm.nodeId,
        template: "default",
        runtime: {
          api_key: createForm.apiKey.trim(),
          base_url: createForm.baseUrl.trim(),
          model: createForm.model.trim(),
        },
      });
      setCreateDialogOpen(false);
      toast.success(t(($) => $.sandboxes_page.create_success));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.sandboxes_page.create_failed));
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center justify-between border-b px-6 py-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{t(($) => $.sandboxes_page.title)}</h1>
          <p className="text-sm text-muted-foreground">{t(($) => $.sandboxes_page.description)}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={handleAddNode} disabled={creatingNode}>
            {creatingNode ? <Loader2 className="mr-2 size-4 animate-spin" /> : <Plus className="mr-2 size-4" />}
            {t(($) => $.sandboxes_page.add_node_action)}
          </Button>
          <Button onClick={() => setCreateDialogOpen(true)} disabled={!hasConnectedNode}>
            <Plus className="mr-2 size-4" />
            {t(($) => $.sandboxes_page.create_action)}
          </Button>
        </div>
      </div>

      <div className="grid gap-4 overflow-auto p-6 lg:grid-cols-[320px_1fr]">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t(($) => $.sandboxes_page.connected_nodes_title)}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {connectedBindings.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t(($) => $.sandboxes_page.no_bound_node)}</p>
            ) : (
              connectedBindings.map((binding) => <NodeCard key={binding.id} binding={binding} />)
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t(($) => $.sandboxes_page.instances_title)}</CardTitle>
          </CardHeader>
          <CardContent>
            {isLoading ? (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> {t(($) => $.sandboxes_page.loading)}
              </div>
            ) : instances.length === 0 ? (
              <div className="flex flex-col items-center justify-center rounded-xl border border-dashed py-16 text-center">
                <Box className="mb-3 size-8 text-muted-foreground" />
                <div className="font-medium">{t(($) => $.sandboxes_page.empty_title)}</div>
                <p className="mt-1 max-w-sm text-sm text-muted-foreground">{t(($) => $.sandboxes_page.empty_description)}</p>
              </div>
            ) : (
              <div className="divide-y rounded-lg border">
                {instances.map((instance) => (
                  <SandboxRow
                    key={instance.id}
                    instance={instance}
                    stopping={stop.isPending && stop.variables === instance.id}
                    resuming={resume.isPending && resume.variables === instance.id}
                    deleting={del.isPending && del.variables === instance.id}
                    onOpen={() => navigation.push(paths.sandboxDetail(instance.id))}
                    onStop={() => stop.mutate(instance.id)}
                    onResume={() => resume.mutate(instance.id)}
                    onDelete={() => del.mutate(instance.id)}
                  />
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

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
                onValueChange={(value) => setCreateForm((current) => ({ ...current, nodeId: value ?? "" }))}
              >
                <SelectTrigger>
                  <SelectValue placeholder={t(($) => $.sandboxes_page.select_node_placeholder)} />
                </SelectTrigger>
                <SelectContent>
                  {connectedBindings.map((binding) => (
                    <SelectItem key={binding.id} value={binding.node_id}>
                      {binding.node_name} ({binding.node_status})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-3">
              <div>
                <div className="text-sm font-medium">{t(($) => $.sandboxes_page.runtime_model_title)}</div>
                <p className="text-xs text-muted-foreground">{t(($) => $.sandboxes_page.runtime_model_hint)}</p>
              </div>
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

      <Dialog open={addDialogOpen} onOpenChange={(open) => dispatchAddNode({ type: "setDialogOpen", open })}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.sandboxes_page.add_node_dialog_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.sandboxes_page.add_node_dialog_description)}</DialogDescription>
          </DialogHeader>
          <code className="max-h-64 overflow-auto rounded-md border bg-muted/50 p-3 text-xs break-all select-all">{setupCommand}</code>
          <DialogFooter>
            <Button variant="outline" onClick={handleCopySetup}>
              {setupCopied ? <Check className="mr-2 size-4" /> : <Copy className="mr-2 size-4" />}
              {setupCopied ? t(($) => $.sandboxes_page.copied_action) : t(($) => $.sandboxes_page.copy_command_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function NodeCard({ binding }: { binding: SandboxBinding }) {
  return (
    <div className="rounded-lg border p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{binding.node_name}</div>
          <div className="truncate text-xs text-muted-foreground">{binding.node_key}</div>
        </div>
        <StatusBadge status={binding.node_status} />
      </div>
    </div>
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
  const canDelete = instance.status !== "stopping" && instance.status !== "resuming";
  const displayName = sandboxDisplayName(instance);

  return (
    <div className="flex items-center justify-between gap-4 p-4">
      <button
        type="button"
        className="min-w-0 flex-1 text-left"
        onClick={onOpen}
      >
        <div className="flex items-center gap-2">
          <span className="truncate font-medium">{displayName}</span>
          <StatusBadge status={instance.status} />
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          {instance.node_name ?? instance.node_id} · {instance.local_ref ?? t(($) => $.sandboxes_page.waiting_local)}
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

function StatusBadge({ status }: { status: string }) {
  const variant = status === "online" || status === "running" ? "default" : status === "failed" ? "destructive" : "secondary";
  return <Badge variant={variant}>{status}</Badge>;
}
