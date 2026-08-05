"use client";

import { useEffect, useState } from "react";
import { ArrowLeft, Box, Check, Copy, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { sandboxDetailOptions } from "@multica/core/sandboxes/queries";
import { useUpdateSandboxMutation } from "@multica/core/sandboxes/mutations";
import {
  buildSandboxRuntimePayload,
  sandboxDisplayName,
  sandboxRuntimeForm,
} from "@multica/core/sandboxes/utils";
import type { SandboxInstance } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";
import { SandboxRuntimeForm } from "./sandbox-runtime-form";
import { SandboxEndpointLinks } from "./sandbox-endpoint-links";

function SandboxIdField({ id }: { id: string }) {
  const { t } = useT("layout");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(timer);
  }, [copied]);

  const handleCopy = () => {
    void copyText(id).then((ok) => {
      if (ok) setCopied(true);
    });
  };

  return (
    <div className="space-y-2">
      <Label htmlFor="sandbox-detail-id">{t(($) => $.sandboxes_page.id_label)}</Label>
      <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
        <code id="sandbox-detail-id" className="min-w-0 flex-1 break-all font-mono text-sm">
          {id}
        </code>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="shrink-0"
          onClick={handleCopy}
          aria-label={t(($) => $.sandboxes_page.copy_id_action)}
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied
            ? t(($) => $.sandboxes_page.copied_action)
            : t(($) => $.sandboxes_page.copy_id_action)}
        </Button>
      </div>
    </div>
  );
}

function SandboxDetailEditor({
  instance,
  wsId,
  instanceId,
}: {
  instance: SandboxInstance;
  wsId: string;
  instanceId: string;
}) {
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const update = useUpdateSandboxMutation(wsId, instanceId);

  const [name, setName] = useState(() => sandboxDisplayName(instance));
  const [runtime, setRuntime] = useState(() => sandboxRuntimeForm(instance));

  const canSave = name.trim().length > 0;
  const isReconfiguring = instance.status === "reconfiguring";

  const handleSave = async () => {
    if (!canSave) return;
    try {
      const runtimePayload = buildSandboxRuntimePayload(runtime);
      await update.mutateAsync({
        name: name.trim(),
        ...(runtimePayload ? { runtime: runtimePayload } : {}),
      });
      toast.success(
        runtimePayload
          ? t(($) => $.sandboxes_page.save_reconfiguring)
          : t(($) => $.sandboxes_page.save_success),
      );
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.sandboxes_page.save_failed));
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center gap-3 border-b px-6 py-4">
        <Button variant="ghost" size="icon" onClick={() => navigation.push(paths.sandboxes())}>
          <ArrowLeft className="size-4" />
        </Button>
        <div className="min-w-0">
          <h1 className="truncate text-xl font-semibold tracking-tight">{sandboxDisplayName(instance)}</h1>
          <p className="text-sm text-muted-foreground">{t(($) => $.sandboxes_page.detail_description)}</p>
        </div>
        <Badge className="ml-auto" variant={instance.status === "running" ? "default" : "secondary"}>
          {instance.status}
        </Badge>
      </div>

      {isReconfiguring && (
        <div className="border-b bg-muted/40 px-6 py-2 text-sm text-muted-foreground">
          {t(($) => $.sandboxes_page.reconfiguring_hint)}
        </div>
      )}

      <div className="overflow-auto p-6">
        <Card className="max-w-2xl">
          <CardHeader>
            <CardTitle className="text-base">{t(($) => $.sandboxes_page.detail_settings_title)}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-6">
            <SandboxIdField id={instance.id} />

            <SandboxEndpointLinks instance={instance} />

            <div className="space-y-2">
              <Label htmlFor="sandbox-detail-name">{t(($) => $.sandboxes_page.name_label)}</Label>
              <Input
                id="sandbox-detail-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>

            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.node_label)}</Label>
              <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
                <div className="font-medium">{instance.node_name ?? instance.node_id}</div>
                {instance.node_key && (
                  <div className="text-xs text-muted-foreground">{instance.node_key}</div>
                )}
              </div>
              <p className="text-xs text-muted-foreground">{t(($) => $.sandboxes_page.node_readonly_hint)}</p>
            </div>

            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.create_template_label)}</Label>
              <div className="rounded-md border bg-muted/30 px-3 py-2 font-mono text-sm break-all">
                {instance.template === "default"
                  ? t(($) => $.sandboxes_page.detail_template_default)
                  : instance.template}
              </div>
              <p className="text-xs text-muted-foreground">
                {t(($) => $.sandboxes_page.template_readonly_hint)}
              </p>
            </div>

            <SandboxRuntimeForm value={runtime} onChange={setRuntime} />

            <div className="flex justify-end">
              <Button onClick={handleSave} disabled={!canSave || update.isPending || isReconfiguring}>
                {update.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
                {t(($) => $.sandboxes_page.save_action)}
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

export function SandboxDetailPage({ instanceId }: { instanceId: string }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const { data: instance, isLoading } = useQuery(sandboxDetailOptions(wsId, instanceId));

  if (isLoading) {
    return (
      <div className="flex h-full min-h-0 flex-col bg-background p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="mt-6 h-64 w-full max-w-2xl" />
      </div>
    );
  }

  if (!instance) {
    return (
      <div className="flex h-full min-h-0 flex-col items-center justify-center bg-background text-muted-foreground">
        <Box className="mb-3 size-10 text-muted-foreground/40" />
        <p className="text-sm">{t(($) => $.sandboxes_page.detail_not_found)}</p>
        <Button className="mt-4" variant="outline" onClick={() => navigation.push(paths.sandboxes())}>
          {t(($) => $.sandboxes_page.back_to_list)}
        </Button>
      </div>
    );
  }

  return (
    <SandboxDetailEditor
      key={`${instance.id}-${instance.updated_at}`}
      instance={instance}
      wsId={wsId}
      instanceId={instanceId}
    />
  );
}
