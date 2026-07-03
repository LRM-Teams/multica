"use client";

import { useMemo, useReducer, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, Check, Copy, Loader2, Plus, Play, RotateCcw, Square, Trash2 } from "lucide-react";
import {
  useCreateSandboxMutation,
  useDeleteSandboxMutation,
  useResumeSandboxMutation,
  useStopSandboxMutation,
} from "@multica/core/sandboxes/mutations";
import { sandboxBindingListOptions, sandboxKeys, sandboxListOptions } from "@multica/core/sandboxes/queries";
import type { SandboxBinding, SandboxInstance } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Badge } from "@multica/ui/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { copyText } from "@multica/ui/lib/clipboard";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import { toast } from "sonner";
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

export function SandboxesPage() {
  const wsId = useWorkspaceId();
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const [modelConfig, setModelConfig] = useState({ apiKey: "", baseUrl: "", model: "" });
  const [addNode, dispatchAddNode] = useReducer(addNodeReducer, initialAddNodeState);
  const { selectedNodeId, dialogOpen: addDialogOpen, setupCommand, setupCopied, creating: creatingNode } = addNode;
  const { data: instances = [], isLoading } = useQuery(sandboxListOptions(wsId));
  const { data: bindings = [] } = useQuery(sandboxBindingListOptions(wsId));

  const onlineBindings = useMemo(() => bindings.filter((b) => b.enabled && b.node_status === "online"), [bindings]);
  const hasOnlineNode = onlineBindings.length > 0;
  const activeNodeId = onlineBindings.some((b) => b.node_id === selectedNodeId) ? selectedNodeId : (onlineBindings[0]?.node_id ?? "");

  const create = useCreateSandboxMutation(wsId, () => ({
    node_id: activeNodeId || undefined,
    runtime: {
      api_key: modelConfig.apiKey,
      base_url: modelConfig.baseUrl,
      model: modelConfig.model,
    },
  }));
  const stop = useStopSandboxMutation(wsId);
  const resume = useResumeSandboxMutation(wsId);
  const del = useDeleteSandboxMutation(wsId);

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
          <Button onClick={() => create.mutate()} disabled={!hasOnlineNode || create.isPending || !modelConfig.apiKey.trim()}>
            {create.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : <Play className="mr-2 size-4" />}
            {t(($) => $.sandboxes_page.create_action)}
          </Button>
        </div>
      </div>

      <div className="grid gap-4 overflow-auto p-6 lg:grid-cols-[320px_1fr]">
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">{t(($) => $.sandboxes_page.runtime_model_title)}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <Input
                type="password"
                placeholder="TEAM_API_KEY"
                value={modelConfig.apiKey}
                onChange={(e) => setModelConfig((v) => ({ ...v, apiKey: e.target.value }))}
              />
              <Input
                placeholder="TEAM_BASE_URL, e.g. https://claude-code.club/openai/v1"
                value={modelConfig.baseUrl}
                onChange={(e) => setModelConfig((v) => ({ ...v, baseUrl: e.target.value }))}
              />
              <Input
                placeholder="TEAM_MODEL, e.g. gpt-5.5"
                value={modelConfig.model}
                onChange={(e) => setModelConfig((v) => ({ ...v, model: e.target.value }))}
              />
              <p className="text-xs text-muted-foreground">{t(($) => $.sandboxes_page.runtime_model_hint)}</p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">{t(($) => $.sandboxes_page.connected_nodes_title)}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {bindings.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t(($) => $.sandboxes_page.no_bound_node)}</p>
              ) : (
                <>
                  <Select value={activeNodeId} onValueChange={(value) => dispatchAddNode({ type: "selectNode", nodeId: value ?? "" })}>
                    <SelectTrigger>
                      <SelectValue placeholder={t(($) => $.sandboxes_page.select_node_placeholder)} />
                    </SelectTrigger>
                    <SelectContent>
                      {onlineBindings.map((binding) => (
                        <SelectItem key={binding.id} value={binding.node_id}>{binding.node_name}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {bindings.map((binding) => (
                    <NodeCard key={binding.id} binding={binding} />
                  ))}
                </>
              )}
            </CardContent>
          </Card>
        </div>

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
  onStop,
  onResume,
  onDelete,
}: {
  instance: SandboxInstance;
  stopping: boolean;
  resuming: boolean;
  deleting: boolean;
  onStop: () => void;
  onResume: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("layout");
  const canStop = instance.status === "running";
  const canResume = instance.status === "stopped";
  const canDelete = instance.status !== "stopping" && instance.status !== "resuming";
  return (
    <div className="flex items-center justify-between gap-4 p-4">
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium">{instance.template}</span>
          <StatusBadge status={instance.status} />
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          {instance.node_name ?? instance.node_id} · {instance.local_ref ?? t(($) => $.sandboxes_page.waiting_local)}
        </div>
        {instance.error && <div className="mt-1 text-xs text-destructive">{instance.error}</div>}
      </div>
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
