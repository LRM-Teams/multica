"use client";

import { use } from "react";
import { useRouter } from "next/navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ProblemEvolutionRunCanvas = lazyNamedRoute(
  () => import("@multica/views/problem-evolution"),
  "ProblemEvolutionRunCanvas",
);

export default function ProblemEvolutionRunRoute({
  params,
}: {
  params: Promise<{ runId: string }>;
}) {
  const { runId } = use(params);
  const router = useRouter();
  const paths = useWorkspacePaths();
  return (
    <ProblemEvolutionRunCanvas
      runId={runId}
      onBack={() => router.push(paths.evolutionSolve())}
    />
  );
}
