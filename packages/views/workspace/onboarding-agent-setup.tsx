"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWSEvent } from "@multica/core/realtime";
import {
  deriveRuntimeHealth,
  runtimeKeys,
  runtimeListOptions,
} from "@multica/core/runtimes";
import type { Workspace } from "@multica/core/types";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { useT } from "../i18n";
import { CliInstallInstructions } from "../onboarding/steps/cli-install-instructions";

/**
 * Mandatory Owner gate when `workspace.onboarding_agent_id` is unset.
 *
 * The previous version only listed already-online Runtimes. New users who
 * skipped (or never finished) Computer install on /onboarding landed here
 * with an empty dropdown and no path to connect a Computer — the workspace
 * shell, including /computers, is blocked by this same gate.
 *
 * When no online Runtime exists, this page embeds the install + setup
 * instructions and listens for `daemon:register` so Create Wendy unlocks
 * in place without navigating into the blocked dashboard.
 */
export function OnboardingAgentSetup({ workspace }: { workspace: Workspace }) {
  const { t } = useT("agents");
  const currentUser = useAuthStore((state) => state.user);
  const queryClient = useQueryClient();
  const { data: members = [] } = useQuery(memberListOptions(workspace.id));
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(workspace.id),
    // Keep probing until a Computer comes online so install/setup can finish
    // without leaving this gate page.
    refetchInterval: (query) => {
      const list = query.state.data ?? [];
      const hasOnline = list.some(
        (runtime) => deriveRuntimeHealth(runtime, Date.now()) === "online",
      );
      return hasOnline ? false : 2000;
    },
  });
  const onlineRuntimes = useMemo(
    () =>
      runtimes.filter(
        (runtime) => deriveRuntimeHealth(runtime, Date.now()) === "online",
      ),
    [runtimes],
  );
  const isOwner = members.some(
    (member) => member.user_id === currentUser?.id && member.role === "owner",
  );
  const [runtimeId, setRuntimeId] = useState("");
  const [model, setModel] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const refreshRuntimes = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: runtimeKeys.all(workspace.id),
    });
  }, [queryClient, workspace.id]);
  useWSEvent("daemon:register", refreshRuntimes);

  // Prefer the first online Runtime as soon as one appears, without
  // overriding a manual selection.
  useEffect(() => {
    if (runtimeId && onlineRuntimes.some((runtime) => runtime.id === runtimeId)) {
      return;
    }
    if (onlineRuntimes[0]) {
      setRuntimeId(onlineRuntimes[0].id);
      return;
    }
    if (runtimeId) {
      setRuntimeId("");
      setModel("");
    }
  }, [onlineRuntimes, runtimeId]);

  if (!isOwner) {
    return (
      <div className="flex min-h-svh items-center justify-center p-6">
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>{t(($) => $.windy.setup_waiting_title)}</CardTitle>
          </CardHeader>
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
        queryClient.invalidateQueries({
          queryKey: workspaceKeys.agents(workspace.id),
        }),
      ]);
    } catch (error) {
      showErrorToast(
        error instanceof Error ? error.message : t(($) => $.windy.setup_failed),
      );
    } finally {
      setSubmitting(false);
    }
  };

  const needsComputer = onlineRuntimes.length === 0;

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
          {needsComputer ? (
            <div
              className="space-y-3"
              data-testid="onboarding-agent-connect-computer"
            >
              <div className="space-y-1">
                <p className="text-sm font-medium">
                  {t(($) => $.windy.setup_connect_title)}
                </p>
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.windy.setup_connect_description)}
                </p>
              </div>
              <CliInstallInstructions workspaceSlug={workspace.slug} />
              <div className="flex items-center gap-2 rounded-lg border bg-muted/30 px-3 py-2.5 text-sm">
                <span
                  aria-hidden
                  className="inline-block size-2 shrink-0 animate-pulse rounded-full bg-success"
                />
                <span className="text-muted-foreground">
                  {t(($) => $.windy.setup_listening)}
                </span>
              </div>
              <div className="flex justify-end">
                <Button disabled>
                  {t(($) => $.windy.setup_create)}
                </Button>
              </div>
            </div>
          ) : (
            <>
              <label className="block space-y-1.5 text-sm font-medium">
                {t(($) => $.windy.setup_runtime)}
                <select
                  className="h-9 w-full rounded-md border bg-background px-3 text-sm"
                  value={runtimeId}
                  onChange={(event) => {
                    setRuntimeId(event.target.value);
                    setModel("");
                  }}
                  aria-label={t(($) => $.windy.setup_runtime)}
                >
                  <option value="">
                    {t(($) => $.windy.setup_runtime_placeholder)}
                  </option>
                  {onlineRuntimes.map((runtime) => (
                    <option key={runtime.id} value={runtime.id}>
                      {runtime.name}
                    </option>
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
              <div className="flex justify-end">
                <Button
                  disabled={!runtimeId || !model.trim() || submitting}
                  onClick={() => void submit()}
                >
                  {submitting
                    ? t(($) => $.windy.setup_creating)
                    : t(($) => $.windy.setup_create)}
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
