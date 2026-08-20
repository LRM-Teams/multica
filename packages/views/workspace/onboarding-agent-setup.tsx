"use client";

import { useCallback, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWSEvent } from "@multica/core/realtime";
import { runtimeKeys, runtimeListOptions } from "@multica/core/runtimes";
import type { Workspace } from "@multica/core/types";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { cn } from "@multica/ui/lib/utils";
import { RuntimeConfigFields } from "../agents/components/runtime-config-fields";
import { useRuntimeConfigSelection } from "../agents/components/use-runtime-config-selection";
import { useT } from "../i18n";
import { CliInstallInstructions } from "../onboarding/steps/cli-install-instructions";

/**
 * Mandatory Owner gate when `workspace.onboarding_agent_id` is unset.
 *
 *   1. Connect a Computer (install + listen for daemon:register)
 *   2. Create Wendy via site-wide Computer → Runtime → Model → Reasoning
 */
export function OnboardingAgentSetup({ workspace }: { workspace: Workspace }) {
  const { t } = useT("agents");
  const currentUser = useAuthStore((state) => state.user);
  const queryClient = useQueryClient();
  const { data: members = [] } = useQuery(memberListOptions(workspace.id));
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(workspace.id),
    refetchInterval: (query) => {
      const list = query.state.data ?? [];
      const hasConnectedComputer = list.some(
        (runtime) => runtime.computer_connected === true,
      );
      return hasConnectedComputer ? false : 2000;
    },
  });
  const connectedRuntimes = useMemo(
    () => runtimes.filter((runtime) => runtime.computer_connected === true),
    [runtimes],
  );
  const isOwner = members.some(
    (member) => member.user_id === currentUser?.id && member.role === "owner",
  );
  const [submitting, setSubmitting] = useState(false);

  const refreshRuntimes = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: runtimeKeys.all(workspace.id),
    });
  }, [queryClient, workspace.id]);
  useWSEvent("daemon:register", refreshRuntimes);

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

  // Step 1 until the server reports a connected Computer; only then Step 2.
  const step: 1 | 2 = connectedRuntimes.length === 0 ? 1 : 2;

  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <Card className="w-full max-w-xl">
        <CardHeader className="space-y-4">
          <SetupStepIndicator activeStep={step} />
          {step === 1 ? (
            <>
              <CardTitle role="heading" aria-level={1}>
                {t(($) => $.windy.setup_step1_title)}
              </CardTitle>
              <p className="text-sm text-muted-foreground">
                {t(($) => $.windy.setup_step1_description)}
              </p>
            </>
          ) : (
            <>
              <CardTitle role="heading" aria-level={1}>
                {t(($) => $.windy.setup_step2_title)}
              </CardTitle>
              <p className="text-sm text-muted-foreground">
                {t(($) => $.windy.setup_step2_description)}
              </p>
            </>
          )}
        </CardHeader>
        <CardContent className="space-y-4">
          {step === 1 ? (
            <div
              className="space-y-3"
              data-testid="onboarding-agent-connect-computer"
            >
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
            </div>
          ) : (
            <WendyCreateForm
              workspaceId={workspace.id}
              runtimes={connectedRuntimes}
              members={members}
              currentUserId={currentUser?.id ?? null}
              submitting={submitting}
              setSubmitting={setSubmitting}
              queryClient={queryClient}
            />
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function WendyCreateForm({
  workspaceId,
  runtimes,
  members,
  currentUserId,
  submitting,
  setSubmitting,
  queryClient,
}: {
  workspaceId: string;
  runtimes: import("@multica/core/types").AgentRuntime[];
  members: import("@multica/core/types").MemberWithUser[];
  currentUserId: string | null;
  submitting: boolean;
  setSubmitting: (v: boolean) => void;
  queryClient: ReturnType<typeof useQueryClient>;
}) {
  const { t } = useT("agents");
  const selection = useRuntimeConfigSelection({
    runtimes,
    currentUserId,
    autoSeedMachine: true,
  });

  const submit = async () => {
    if (!selection.runtimeId || !selection.model.trim()) return;
    setSubmitting(true);
    try {
      await api.ensureWindy(
        selection.runtimeId,
        selection.model.trim(),
        selection.thinkingLevel || undefined,
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: workspaceKeys.list() }),
        queryClient.invalidateQueries({
          queryKey: workspaceKeys.agents(workspaceId),
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

  return (
    <div data-testid="onboarding-agent-create-wendy" className="space-y-4">
      <RuntimeConfigFields
        runtimes={runtimes}
        members={members}
        currentUserId={currentUserId}
        machineId={selection.machineId}
        onMachineSelect={selection.selectMachine}
        machineRuntimes={selection.machineRuntimes}
        runtimeId={selection.runtimeId}
        onRuntimeSelect={selection.selectRuntime}
        model={selection.model}
        onModelChange={selection.selectModel}
        thinkingLevel={selection.thinkingLevel}
        onThinkingChange={selection.selectThinking}
        modelRequired
        disabled={submitting}
      />
      <div className="flex justify-end">
        <Button
          disabled={
            !selection.runtimeId || !selection.model.trim() || submitting
          }
          onClick={() => void submit()}
        >
          {submitting
            ? t(($) => $.windy.setup_creating)
            : t(($) => $.windy.setup_create)}
        </Button>
      </div>
    </div>
  );
}

function SetupStepIndicator({ activeStep }: { activeStep: 1 | 2 }) {
  const { t } = useT("agents");
  const steps = [
    { n: 1 as const, label: t(($) => $.windy.setup_step_computer) },
    { n: 2 as const, label: t(($) => $.windy.setup_step_wendy) },
  ];
  return (
    <ol
      className="flex items-center gap-2"
      data-testid="onboarding-agent-setup-steps"
      aria-label={t(($) => $.windy.setup_steps_label)}
    >
      {steps.map((step, index) => {
        const done = activeStep > step.n;
        const current = activeStep === step.n;
        return (
          <li key={step.n} className="flex min-w-0 flex-1 items-center gap-2">
            {index > 0 ? (
              <span
                aria-hidden
                className={cn(
                  "h-px w-4 shrink-0 sm:w-8",
                  done || current ? "bg-primary/60" : "bg-border",
                )}
              />
            ) : null}
            <span
              className={cn(
                "flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
                done || current
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground",
              )}
              aria-current={current ? "step" : undefined}
            >
              {step.n}
            </span>
            <span
              className={cn(
                "truncate text-xs font-medium",
                current ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {step.label}
            </span>
          </li>
        );
      })}
    </ol>
  );
}
