"use client";

import { useEffect } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";

/** Legacy Agents list → Members Directory (ADR 0013). */
export default function AgentsRedirectPage() {
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  useEffect(() => {
    navigation.replace(paths.members());
  }, [navigation, paths]);
  return null;
}
