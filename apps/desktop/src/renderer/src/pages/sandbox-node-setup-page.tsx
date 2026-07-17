import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { SandboxNodeSetupPage as SharedSandboxNodeSetupPage } from "@multica/views/sandboxes";
import { sandboxNodeListOptions } from "@multica/core/sandboxes/queries";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function SandboxNodeSetupPage() {
  const { nodeId } = useParams<{ nodeId: string }>();
  const { data: nodes = [] } = useQuery(sandboxNodeListOptions());
  const node = nodes.find((item) => item.id === nodeId);

  useDocumentTitle(node?.name ? `${node.name} setup` : "Sandbox setup");

  if (!nodeId) return null;
  return <SharedSandboxNodeSetupPage nodeId={nodeId} />;
}
