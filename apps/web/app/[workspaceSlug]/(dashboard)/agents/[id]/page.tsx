"use client";

import { useEffect } from "react";
import { use } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";

/** Legacy Agent detail → Members Directory selection (ADR 0013). */
export default function AgentDetailRedirectPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  useEffect(() => {
    navigation.replace(paths.members({ kind: "agent", id }));
  }, [navigation, paths, id]);
  return null;
}
