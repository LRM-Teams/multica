"use client";

import { use, useEffect } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";

/** Legacy member detail → Members Directory selection. */
export default function MemberDetailRedirectPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  useEffect(() => {
    navigation.replace(paths.members({ kind: "user", id }));
  }, [navigation, paths, id]);
  return null;
}
