"use client";

import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { deriveRuntimeHealth, runtimeListOptions } from "@multica/core/runtimes";
import type { Workspace } from "@multica/core/types";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { useT } from "../i18n";

export function OnboardingAgentSetup({ workspace }: { workspace: Workspace }) {
  const { t } = useT("agents");
  const currentUser = useAuthStore((state) => state.user);
  const queryClient = useQueryClient();
  const { data: members = [] } = useQuery(memberListOptions(workspace.id));
  const { data: runtimes = [] } = useQuery(runtimeListOptions(workspace.id));
  const onlineRuntimes = useMemo(
    () => runtimes.filter((runtime) => deriveRuntimeHealth(runtime, Date.now()) === "online"),
    [runtimes],
  );
  const isOwner = members.some(
    (member) => member.user_id === currentUser?.id && member.role === "owner",
  );
  const [runtimeId, setRuntimeId] = useState("");
  const [model, setModel] = useState("");
  const [submitting, setSubmitting] = useState(false);

  if (!isOwner) {
    return (
      <div className="flex min-h-svh items-center justify-center p-6">
        <Card className="w-full max-w-lg">
          <CardHeader><CardTitle>{t(($) => $.windy.setup_waiting_title)}</CardTitle></CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            {t(($) => $.windy.setup_waiting_description)}
          </CardContent>
        </Card>
      </div>
    );
  }

  const submit = async () => {
    setSubmitting(true);
    try {
      await api.ensureWindy(runtimeId, model.trim());
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: workspaceKeys.list() }),
        queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(workspace.id) }),
      ]);
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.windy.setup_failed));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <Card className="w-full max-w-xl">
        <CardHeader>
          <CardTitle>{t(($) => $.windy.setup_title)}</CardTitle>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.windy.setup_description)}
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <label className="block space-y-1.5 text-sm font-medium">
            {t(($) => $.windy.setup_runtime)}
            <select
              className="h-9 w-full rounded-md border bg-background px-3 text-sm"
              value={runtimeId}
              onChange={(event) => setRuntimeId(event.target.value)}
            >
              <option value="">{t(($) => $.windy.setup_runtime_placeholder)}</option>
              {onlineRuntimes.map((runtime) => (
                <option key={runtime.id} value={runtime.id}>{runtime.name}</option>
              ))}
            </select>
          </label>
          <ModelDropdown
            runtimeId={runtimeId || null}
            runtimeOnline={Boolean(runtimeId)}
            value={model}
            onChange={setModel}
            disabled={!runtimeId}
            required
          />
          {onlineRuntimes.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t(($) => $.windy.setup_runtime_offline)}</p>
          ) : null}
          <div className="flex justify-end">
            <Button disabled={!runtimeId || !model.trim() || submitting} onClick={() => void submit()}>
              {submitting ? t(($) => $.windy.setup_creating) : t(($) => $.windy.setup_create)}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
