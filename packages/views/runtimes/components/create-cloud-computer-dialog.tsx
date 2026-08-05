"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { SandboxBinding, SandboxInstance } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { sandboxBindingListOptions } from "@multica/core/sandboxes/queries";
import { useT } from "../../i18n/use-t";
import { CreateDockerContainerDialog } from "../../sandboxes/components/create-docker-container-dialog";

const EMPTY_BINDINGS: SandboxBinding[] = [];

/**
 * Computers → Add computer → Cloud computer step.
 * Creates a Docker container on a connected sandbox node (same API as the
 * Sandboxes → Docker tab).
 */
export function CreateCloudComputerDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated?: (instance: SandboxInstance) => void;
}) {
  const wsId = useWorkspaceId();
  const { t } = useT("runtimes");
  const { data: bindings } = useQuery(sandboxBindingListOptions(wsId));

  const connectedBindings = useMemo(
    () => (bindings ?? EMPTY_BINDINGS).filter((binding) => binding.enabled),
    [bindings],
  );

  return (
    <CreateDockerContainerDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      bindings={connectedBindings}
      labels={{
        title: t(($) => $.create_cloud_computer.title),
        description: t(($) => $.create_cloud_computer.description),
        submit: t(($) => $.create_cloud_computer.submit),
        successToast: t(($) => $.create_cloud_computer.toast_created),
        failedToast: t(($) => $.create_cloud_computer.toast_create_failed),
      }}
      onCreated={onCreated}
    />
  );
}
