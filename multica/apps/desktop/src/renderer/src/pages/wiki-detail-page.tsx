import { useParams } from "react-router-dom";
import { KnowledgePageView } from "@multica/views/knowledge";
import { useDocumentTitle } from "@/hooks/use-document-title";

export function WikiDetailPage() {
  const { id } = useParams<{ id: string }>();
  useDocumentTitle("Knowledge");
  if (!id) return null;
  return <KnowledgePageView pageId={id} />;
}
