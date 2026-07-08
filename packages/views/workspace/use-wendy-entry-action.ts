"use client";

import { useCallback, useMemo } from "react";
import type { Agent } from "@multica/core/types";
import { useOpenDM } from "../common/use-open-dm";
import { findWindyAgent } from "./windy-setup-detection";

export function useWindyEntryAction(_wsId: string, agents: readonly Agent[]) {
  const { openDM, isPending: openingDM } = useOpenDM();
  const windyAgent = useMemo(() => findWindyAgent(agents), [agents]);
  const hasConfiguredWendy = !!windyAgent?.runtime_id;

  const openWindy = useCallback(async () => {
    if (!windyAgent?.runtime_id || openingDM) return false;
    const dm = await openDM({ peer_type: "agent", peer_id: windyAgent.id });
    return !!dm;
  }, [openingDM, openDM, windyAgent]);

  return {
    hasWendy: !!windyAgent,
    hasConfiguredWendy,
    isPending: openingDM,
    openWindy,
    windyAgent,
  };
}
