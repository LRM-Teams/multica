import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { AgentDetailPage as SharedAgentDetailPage } from "@multica/views/agents";
import { agentDetailOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function AgentDetailPage() {
  const { id } = useParams<{ id: string }>();
  const wsId = useWorkspaceId();
  // Title from GET /api/agents/:id so archived agents still resolve (LRM-410).
  const { data: agent } = useQuery({
    ...agentDetailOptions(wsId, id ?? ""),
    enabled: !!wsId && !!id,
  });

  useDocumentTitle(agent?.name ?? "Agent");

  if (!id) return null;
  return <SharedAgentDetailPage agentId={id} />;
}
