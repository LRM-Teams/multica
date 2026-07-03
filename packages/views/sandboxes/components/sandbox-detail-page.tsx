"use client";

import { useEffect, useState } from "react";
import { ArrowLeft, Box, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { sandboxDetailOptions } from "@multica/core/sandboxes/queries";
import { useUpdateSandboxMutation } from "@multica/core/sandboxes/mutations";
import { sandboxDisplayName, sandboxRuntime } from "@multica/core/sandboxes/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n/use-t";

type RuntimeFormState = {
  apiKey: string;
  baseUrl: string;
  model: string;
};

function buildRuntimePayload(form: RuntimeFormState) {
  const payload: Record<string, string> = {};
  const apiKey = form.apiKey.trim();
  const baseUrl = form.baseUrl.trim();
  const model = form.model.trim();
  if (apiKey) payload.api_key = apiKey;
  if (baseUrl) payload.base_url = baseUrl;
  if (model) payload.model = model;
  return payload;
}

export function SandboxDetailPage({ instanceId }: { instanceId: string }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const { t } = useT("layout");
  const { data: instance, isLoading } = useQuery(sandboxDetailOptions(wsId, instanceId));
  const update = useUpdateSandboxMutation(wsId, instanceId);

  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState<RuntimeFormState>({ apiKey: "", baseUrl: "", model: "" });

  useEffect(() => {
    if (!instance) return;
    setName(sandboxDisplayName(instance));
    const currentRuntime = sandboxRuntime(instance);
    setRuntime(currentRuntime);
  }, [instance]);

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

  const canSave = name.trim().length > 0;

  const handleSave = async () => {
    if (!canSave) return;
    try {
      const runtimePayload = buildRuntimePayload(runtime);
      await update.mutateAsync({
        name: name.trim(),
        ...(Object.keys(runtimePayload).length > 0 ? { runtime: runtimePayload } : {}),
      });
      toast.success(
        Object.keys(runtimePayload).length > 0
          ? t(($) => $.sandboxes_page.save_reconfiguring)
          : t(($) => $.sandboxes_page.save_success),
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.sandboxes_page.save_failed));
    }
  };

  const isReconfiguring = instance.status === "reconfiguring";

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

            <div className="space-y-3">
              <div>
                <div className="text-sm font-medium">{t(($) => $.sandboxes_page.runtime_model_title)}</div>
                <p className="text-xs text-muted-foreground">{t(($) => $.sandboxes_page.runtime_model_optional_hint)}</p>
              </div>
              <Input
                type="password"
                placeholder={t(($) => $.sandboxes_page.api_key_placeholder)}
                value={runtime.apiKey}
                onChange={(e) => setRuntime((current) => ({ ...current, apiKey: e.target.value }))}
              />
              <Input
                placeholder={t(($) => $.sandboxes_page.base_url_placeholder)}
                value={runtime.baseUrl}
                onChange={(e) => setRuntime((current) => ({ ...current, baseUrl: e.target.value }))}
              />
              <Input
                placeholder={t(($) => $.sandboxes_page.model_placeholder)}
                value={runtime.model}
                onChange={(e) => setRuntime((current) => ({ ...current, model: e.target.value }))}
              />
            </div>

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
