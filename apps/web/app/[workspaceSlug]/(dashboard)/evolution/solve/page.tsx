"use client";

import { useRouter } from "next/navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { lazyNamedRoute } from "@/lib/lazy-route";

const ProblemEvolutionSolvePage = lazyNamedRoute(
  () => import("@multica/views/problem-evolution"),
  "ProblemEvolutionSolvePage",
);

export default function ProblemEvolutionSolveRoute() {
  const router = useRouter();
  const paths = useWorkspacePaths();
  return (
    <ProblemEvolutionSolvePage
      onOpenRun={(runId) => router.push(paths.evolutionSolveRun(runId))}
    />
  );
}
