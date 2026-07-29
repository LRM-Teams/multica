import { useParams } from "react-router-dom";
import { ResearchSessionPage } from "@multica/views/research";

export function DesktopResearchSessionPage() {
  const { id } = useParams<{ id: string }>();
  if (!id) return null;
  return <ResearchSessionPage sessionId={id} />;
}
