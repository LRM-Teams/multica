import { Navigate } from "react-router-dom";

/**
 * Former runtime-detail orphan. Frank 2026-08-02: ops + sharing live on the
 * computers/runtimes list detail panel — redirect old deep links.
 */
export function RuntimeDetailPage() {
  return <Navigate to=".." replace relative="path" />;
}
