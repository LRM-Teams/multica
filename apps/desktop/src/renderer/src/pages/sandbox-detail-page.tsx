import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { SandboxDetailPage as SharedSandboxDetailPage } from "@multica/views/sandboxes";
import { useWorkspaceId } from "@multica/core/hooks";
import { sandboxDetailOptions } from "@multica/core/sandboxes/queries";
import { sandboxDisplayName } from "@multica/core/sandboxes/utils";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function SandboxDetailPage() {
  const { instanceId } = useParams<{ instanceId: string }>();
  const wsId = useWorkspaceId();
  const { data: instance } = useQuery(sandboxDetailOptions(wsId, instanceId ?? ""));

  useDocumentTitle(instance ? sandboxDisplayName(instance) : "Sandbox");

  if (!instanceId) return null;
  return <SharedSandboxDetailPage instanceId={instanceId} />;
}
