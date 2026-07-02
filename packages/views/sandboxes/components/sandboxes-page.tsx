"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Box, Loader2, Play, RotateCcw, Square, Trash2 } from "lucide-react";
import {
  useCreateSandboxMutation,
  useDeleteSandboxMutation,
  useResumeSandboxMutation,
  useStopSandboxMutation,
} from "@multica/core/sandboxes/mutations";
import { sandboxBindingListOptions, sandboxListOptions } from "@multica/core/sandboxes/queries";
import type { SandboxInstance } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Badge } from "@multica/ui/components/ui/badge";
import { useWorkspaceId } from "@multica/core/hooks";

export function SandboxesPage() {
  const wsId = useWorkspaceId();
  const [modelConfig, setModelConfig] = useState({ apiKey: "", baseUrl: "", model: "" });
  const { data: instances = [], isLoading } = useQuery(sandboxListOptions(wsId));
  const { data: bindings = [] } = useQuery(sandboxBindingListOptions(wsId));

  const create = useCreateSandboxMutation(wsId, () => ({
    api_key: modelConfig.apiKey,
    base_url: modelConfig.baseUrl,
    model: modelConfig.model,
  }));
  const stop = useStopSandboxMutation(wsId);
  const resume = useResumeSandboxMutation(wsId);
  const del = useDeleteSandboxMutation(wsId);

  const hasOnlineNode = bindings.some((b) => b.enabled && b.node_status === "online");

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center justify-between border-b px-6 py-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Sandboxes</h1>
          <p className="text-sm text-muted-foreground">
            Create internal sandboxes through the shared sandbox node connector.
          </p>
        </div>
        <Button onClick={() => create.mutate()} disabled={!hasOnlineNode || create.isPending || !modelConfig.apiKey.trim()}>
          {create.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : <Play className="mr-2 size-4" />}
          Create sandbox
        </Button>
      </div>

      <div className="grid gap-4 overflow-auto p-6 lg:grid-cols-[320px_1fr]">
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Runtime model</CardTitle>
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
              <p className="text-xs text-muted-foreground">
                Model settings are sent to the sandbox runtime when a sandbox is created.
              </p>
            </CardContent>
          </Card>

          <Card>
          <CardHeader>
            <CardTitle className="text-base">Connected nodes</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {bindings.length === 0 ? (
              <p className="text-sm text-muted-foreground">No sandbox node is bound to this workspace yet.</p>
            ) : (
              bindings.map((binding) => (
                <div key={binding.id} className="rounded-lg border p-3">
                  <div className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium">{binding.node_name}</div>
                      <div className="truncate text-xs text-muted-foreground">{binding.node_key}</div>
                    </div>
                    <StatusBadge status={binding.node_status} />
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>
        </div>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Sandbox instances</CardTitle>
          </CardHeader>
          <CardContent>
            {isLoading ? (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> Loading sandboxes...
              </div>
            ) : instances.length === 0 ? (
              <div className="flex flex-col items-center justify-center rounded-xl border border-dashed py-16 text-center">
                <Box className="mb-3 size-8 text-muted-foreground" />
                <div className="font-medium">No sandboxes yet</div>
                <p className="mt-1 max-w-sm text-sm text-muted-foreground">
                  Create one to ask the internal sandbox node to provision an isolated local environment.
                </p>
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
          {instance.node_name ?? instance.node_id} · {instance.local_ref ?? "waiting for local sandbox"}
        </div>
        {instance.error && <div className="mt-1 text-xs text-destructive">{instance.error}</div>}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {canResume ? (
          <Button size="sm" variant="outline" disabled={resuming} onClick={onResume}>
            {resuming ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <RotateCcw className="mr-2 size-3.5" />}
            Resume
          </Button>
        ) : (
          <Button size="sm" variant="outline" disabled={!canStop || stopping} onClick={onStop}>
            {stopping ? <Loader2 className="mr-2 size-3.5 animate-spin" /> : <Square className="mr-2 size-3.5" />}
            Stop
          </Button>
        )}
        <Button size="sm" variant="ghost" disabled={!canDelete || deleting} onClick={onDelete}>
          <Trash2 className="mr-2 size-3.5" /> Delete
        </Button>
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const variant = status === "online" || status === "running" ? "default" : status === "failed" ? "destructive" : "secondary";
  return <Badge variant={variant}>{status}</Badge>;
}
